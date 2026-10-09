#!/usr/bin/env python3
"""Build and exercise the shipped scratch image on loopback with disposable data."""
import argparse
import base64
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import struct
import subprocess
import sys
import tempfile
import time
import urllib.request
import zlib

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'ops'))
import ops


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--ca-bundle', type=Path, default=Path('/etc/ssl/certs/ca-certificates.crt'))
    args = parser.parse_args()
    checks, containers, credentials = [], [], []
    docker_env = os.environ.copy()
    image_tag = 'folio-local-acceptance:' + secrets.token_hex(6)
    uid, gid = (os.getuid(), os.getgid()) if os.getuid() else (65532, 65532)

    def docker(*arguments):
        result = subprocess.run(['docker', *arguments], capture_output=True, text=True, timeout=60, env=docker_env)
        if result.returncode:
            # Do not include arguments: they can contain disposable credentials.
            message = result.stderr
            for token in credentials: message = message.replace(token, '[redacted]')
            raise AssertionError('Docker command failed: ' + arguments[0] + '\n' + message[:2500])
        return result.stdout.strip()

    def completed(message):
        checks.append(message); print('PASS ' + message, flush=True)

    with tempfile.TemporaryDirectory(prefix='folio-docker-') as tmp:
        root = Path(tmp)
        config = root / 'docker-config';config.mkdir()
        docker_env['DOCKER_CONFIG'] = str(config)
        context = root / 'image';context.mkdir()
        binary = args.binary.resolve()
        binary_hash = hashlib.sha256(binary.read_bytes()).hexdigest()
        ops.prepare_image(binary, binary_hash, args.ca_bundle,
                          hashlib.sha256(args.ca_bundle.read_bytes()).hexdigest(), context / 'dist', 'amd64')
        shutil.copyfile(ROOT / 'ops/deploy/Dockerfile', context / 'Dockerfile')
        try:
            docker('build', '--network=none', '--tag', image_tag, str(context))
            image_id = docker('image', 'inspect', '--format', '{{.Id}}', image_tag)
            assert docker('image', 'inspect', '--format', '{{.Config.User}}', image_id) == '65532:65532'
            completed('shipped scratch Dockerfile builds from hash-verified static ELF and CA bundle')

            def create(name, paused=False):
                data = root / name;data.mkdir(mode=0o700)
                if os.getuid() == 0:
                    os.chown(data, uid, gid)  # This disposable fixture only.
                mount = 'type=bind,src=' + str(data) + ',dst=/data'
                docker('run', '--rm', '--network=none', '--user', f'{uid}:{gid}', '--mount', mount,
                       image_id, 'init', '--data', '/data')
                token = (data / 'token').read_text().strip();credentials.append(token)
                cname = 'folio-local-' + name + '-' + secrets.token_hex(5)
                containers.append(cname)
                command = ['run', '--detach', '--name', cname, '--read-only', '--cap-drop=ALL',
                           '--security-opt=no-new-privileges', '--user', f'{uid}:{gid}', '--mount', mount,
                           '--publish', '127.0.0.1::8080', image_id, 'serve', '--data', '/data', '--addr', '0.0.0.0:8080']
                if paused: command.append('--pause-schedules')
                docker(*command)
                return {'name': cname, 'token': token, 'data': data}

            def ready(instance):
                instance['url'] = 'http://' + docker('port', instance['name'], '8080/tcp').splitlines()[0]
                for _ in range(100):
                    try:
                        with urllib.request.urlopen(instance['url'] + '/readyz', timeout=1) as r:
                            if r.status == 200: return
                    except (OSError, ValueError): pass
                    time.sleep(.05)
                raise AssertionError('container did not become ready')

            def call(instance, operation, payload=None):
                request = urllib.request.Request(instance['url'] + '/api/op/' + operation,
                    data=json.dumps(payload or {}, separators=(',', ':')).encode(),
                    headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + instance['token']})
                with urllib.request.urlopen(request, timeout=10) as response:
                    result = json.load(response)
                    assert result['ok'], 'container API operation failed'
                    return result['data']

            source = create('source');ready(source)
            assert docker('inspect', '--format', '{{.Config.User}}', source['name']) == f'{uid}:{gid}'
            assert docker('inspect', '--format', '{{.HostConfig.ReadonlyRootfs}}', source['name']) == 'true'
            docker('exec', source['name'], '/folio', 'healthcheck')
            completed('non-root container runs with read-only root, dropped capabilities, loopback publishing and real healthcheck')
            post = call(source, 'posts.create', {'title': 'Fictional container acceptance', 'slug': 'container-acceptance',
                'markdown': 'Pinned container revision one.', 'idempotency_key': 'container-create'})['post']
            schedule = call(source, 'schedules.create', {'post_id': post['id'], 'expected_revision': 1,
                'publish_at': (datetime.now(timezone.utc) + timedelta(seconds=3)).isoformat(),
                'confirm': True, 'idempotency_key': 'container-schedule'})['schedule']
            newer = call(source, 'posts.update', {'id': post['id'], 'expected_revision': 1,
                'title': post['title'], 'slug': post['slug'], 'markdown': 'Revision two remains private.',
                'idempotency_key': 'container-update'})['post']
            docker('stop', '--time', '10', source['name']);time.sleep(3.1)
            docker('start', source['name']);ready(source)
            for _ in range(50):
                if call(source, 'schedules.get', {'id': schedule['id']})['schedule']['status'] == 'published': break
                time.sleep(.05)
            record = call(source, 'posts.get', {'id': post['id']})
            assert record['post']['revision'] == 2 and record['post']['markdown'] == newer['markdown']
            assert record['live']['revision'] == 1 and record['live']['markdown'] == post['markdown']
            docker('restart', '--time', '10', source['name']);ready(source)
            events = call(source, 'audit.list')['events']
            assert sum(e['operation'] == 'schedules.publish' for e in events) == 1
            completed('SIGTERM/restart preserves SQLite state; overdue pinned revision publishes once while newer draft stays private')

            def chunk(kind, data):
                return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))
            png = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', 1, 1, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(b'\x00\x80\xa0\xc0')) + chunk(b'IEND', b'')
            media = call(source, 'media.upload', {'name': 'fictional.png', 'base64': base64.b64encode(png).decode(),
                                                'idempotency_key': 'container-media'})['media']
            backup = call(source, 'backup.export')
            target = create('restore', paused=True);ready(target)
            assert call(target, 'system.info')['scheduler']['paused']
            call(target, 'backup.restore', {'backup': backup, 'confirm': True, 'idempotency_key': 'container-restore'})
            restored = call(target, 'posts.get', {'id': post['id']})
            assert restored == record
            with urllib.request.urlopen(target['url'] + media['url'], timeout=10) as response:
                assert response.read() == png
            assert call(target, 'schedules.get', {'id': schedule['id']})['schedule']['status'] == 'published'
            assert target['token'] != source['token']
            assert all(token not in json.dumps(backup) for token in [target['token'], source['token']])
            completed('container backup/paused restore preserves exact draft/live revisions, pinned schedules and media bytes with independent credentials')
            for instance in [source, target]:
                assert instance['token'] not in docker('logs', instance['name'])
                docker('stop', '--time', '10', instance['name'])
                assert docker('inspect', '--format', '{{.State.ExitCode}}', instance['name']) == '0'
            completed('owned containers shut down cleanly; credentials absent from daemon logs')
        finally:
            for name in containers:
                subprocess.run(['docker', 'rm', '--force', name], capture_output=True, timeout=15, env=docker_env)
            subprocess.run(['docker', 'image', 'rm', image_tag], capture_output=True, timeout=15, env=docker_env)
    summary = {'status': 'passed', 'checks': checks, 'container_run': True, 'image_id': image_id,
               'binary_sha256': binary_hash, 'runtime_uid': uid, 'public_deployment': False}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(summary, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()

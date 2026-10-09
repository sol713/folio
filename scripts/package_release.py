#!/usr/bin/env python3
"""Package allowlisted release source or static Linux executable; no runtime data."""
import argparse, gzip, hashlib, io, json, pathlib, tarfile
ROOT=pathlib.Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--output',type=pathlib.Path,required=True)
p.add_argument('--binary',type=pathlib.Path)
a=p.parse_args()
roots=['LICENSE','THIRD_PARTY_NOTICES.md','README.md','AGENTS.md','.gitignore','go.mod','go.sum','assets.go','cmd','internal','web','docs','scripts','ops','third_party'] if not a.binary else ['LICENSE','THIRD_PARTY_NOTICES.md','README.md','docs','ops','third_party']
files={}
for name in roots:
 path=ROOT/name
 for f in sorted(path.rglob('*')) if path.is_dir() else [path]:
  rel=f.relative_to(ROOT)
  if '__pycache__' in rel.parts or f.suffix in {'.pyc','.log','.db','.db-wal','.db-shm'}: continue
  if f.is_symlink(): raise SystemExit('Refusing symlink: '+str(rel))
  if f.is_file(): files[str(rel)]=f
if a.binary:
 if not a.binary.is_file() or a.binary.is_symlink(): raise SystemExit('Missing regular binary')
 files['folio-linux-amd64']=a.binary
manifest={'format':'folio-release','version':'0.2.0','kind':'linux-amd64' if a.binary else 'source','files':{n:{'bytes':f.stat().st_size,'sha256':hashlib.sha256(f.read_bytes()).hexdigest()} for n,f in sorted(files.items())}}
a.output.parent.mkdir(parents=True,exist_ok=True)
with a.output.open('wb') as raw,gzip.GzipFile(fileobj=raw,mode='wb',mtime=0) as gz,tarfile.open(fileobj=gz,mode='w') as tar:
 for name,f in sorted(files.items()):
  data=f.read_bytes();info=tarfile.TarInfo('folio/'+name);info.size=len(data);info.mode=0o755 if name=='folio-linux-amd64' else 0o644;tar.addfile(info,io.BytesIO(data))
 data=(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n').encode();info=tarfile.TarInfo('folio/RELEASE-MANIFEST.json');info.size=len(data);info.mode=0o644;tar.addfile(info,io.BytesIO(data))
with tarfile.open(a.output) as tar:
 for member in tar.getmembers():
  if member.name=='folio/RELEASE-MANIFEST.json': continue
  data=tar.extractfile(member).read();assert hashlib.sha256(data).hexdigest()==manifest['files'][member.name[6:]]['sha256']
print(json.dumps({'path':str(a.output.resolve()),'bytes':a.output.stat().st_size,'sha256':hashlib.sha256(a.output.read_bytes()).hexdigest(),'files':len(files),'kind':manifest['kind']}))

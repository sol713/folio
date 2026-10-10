# Local draft recovery / 本机草稿恢复

This feature is a browser editing safety net. The Go service remains the sole
authority for article IDs, revisions, permissions, saving and publication.
Restoring a copy changes only the unsaved editor. It never saves or publishes.

## Privacy and consent

- Default: encrypted copies in this tab's `sessionStorage`, expiring after 24 hours.
  A completed copy survives refresh. Closing the tab normally ends this storage.
- Explicit option: encrypted `localStorage` copies retained for seven days after
  their last update, for recovery after closing a tab. The user must opt in.
- HKDF domain separation binds a non-extractable AES-GCM key and opaque index to
  origin, server instance ID, authenticated role and the current access token.
  Recovery requires Web Crypto in a secure context (localhost or HTTPS).
  No token, encryption key, article title, article ID or Markdown is stored in
  plaintext by recovery. Credentials keep the pre-existing session-only policy.
  This does not defend against compromised same-origin code or access to an
  already unlocked browser profile: the existing session bearer token can
  authorize server calls and derive recovery access. Do not share unlocked Studio.
- Recovery is available only after current server authentication and direct
  draft-writing permission. Another token, role or instance cannot list/decrypt
  the copies. Shared role tokens continue to represent the same existing identity.
- Lock clears this tab's session copies and in-memory keys. Opted-in persistent
  encrypted copies remain until expiry or explicit clearing; the UI says so.
  Re-login with the same still-authorized token is required. Token rotation does
  not transfer recovery access. Copies are not a substitute for a server backup.
  Expiry makes copies unavailable; known expired ciphertext is removed on later
  authenticated Studio use, including across rotated token namespaces. There is
  no background timer while the browser is closed. Unknown versions are retained.

## Identity, conflicts and retry receipts

Each editing session writes its own random slot. Refresh, duplicated tabs and
parallel tabs never share a mutable article slot. Candidates are selected by
article ID (or new-story status) only after decryption. Different copies remain
available for explicit review rather than last-writer-wins replacement.

The copy contains all editable fields, source revision/fingerprint, cursor, and
any exact pending draft-create/update payload and retry key. A server-confirmed
save removes only the slot owned by the active writer. Other writers' copies are
not implicitly deleted. Recovery compares current server content and requires
extra acknowledgement for a changed base; the server's revision guard still
protects the later explicit Save. Unconfirmed writes retain their exact retry
identity, so a recovered new draft cannot silently duplicate a committed create.
After replaying a recovered retry receipt, the editor re-reads the current server
head: the receipt can be historical. Divergent restored text remains unsaved and
uses the latest revision for its next explicit Save. If that read fails, the
acknowledged ID is retained, text stays dirty and the UI reports the failed check.

All asynchronous storage and restoration work is bound to its editor and login.
Locking, navigation, disabling or clearing invalidates pending work. A changed
editor buffer is not replaced by a late recovery response. Each recovery review
also captures the dialog generation: Cancel, Escape, backdrop close or a later
dialog invalidates it. The normal selection-to-comparison transition explicitly
adopts its new generation. Cancellation keeps encrypted copies and editor state. UI content previews
are escaped plain text, including untrusted Markdown and metadata.

## Controls and limits

The editor distinguishes local-copy status from server-save status. It exposes
tab-only protection, seven-day opt-in, disable, review and clear controls.
Changing mode updates other active writers. Turning off stops new copies and
removes the active writer slot; previously retained separate copies can still
be reviewed or explicitly cleared. Selecting tab-only turns off seven-day
retention for new copies; existing encrypted copies keep their original expiry.
Clearing is scoped to this site/identity and explicitly disables recovery; a
cross-tab notification also clears this identity's active session copies.

Limits are 20 copies, 512 KiB of serialized plaintext per copy and 4 MiB encoded
storage per identity/storage area. Only expired supported-version records are
pruned automatically. Unknown, damaged or over-limit copies are preserved for
explicit clearing, with a readable warning. Storage/crypto unavailability and
quota failures never produce a successful-copy label or block explicit server
Save. Markdown export remains available.

Only completed local writes can be recovered. Browser data clearing/eviction,
private-mode policies or interruption of an unfinished crypto write can remove
or prevent copies. Existing unload warnings remain. Offline editing can update
an already authenticated local copy; after an offline refresh, reconnection and
server authentication are required before private recovery is shown.

本机恢复只改未保存的编辑区，不写服务器、不发布。默认仅当前标签页加密保存，
关页恢复须明确启用七天保留。锁定清除本标签页副本及内存密钥；已启用的持久副本
仍为密文，须用同一有效身份登录才能恢复。不同标签页保留独立副本，恢复先审阅
与当前服务器版本的差异。容量、配额、格式或存储故障会提示，不能冒充已保存。

## Executed checks

See [the machine-readable record](LOCAL-RECOVERY-VERIFICATION.json). Run the
real recovery acceptance with a rebuilt executable and Python Playwright 1.62.0:

```sh
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o dist/folio ./cmd/folio
node --test web/tests/*.test.cjs
python3 scripts/local_recovery_e2e.py --binary dist/folio --chromium /path/to/chromium --report /tmp/folio-local-recovery-results.json
```

Browser platform references: [sessionStorage](https://developer.mozilla.org/en-US/docs/Web/API/Window/sessionStorage),
[Web Crypto deriveKey](https://developer.mozilla.org/en-US/docs/Web/API/SubtleCrypto/deriveKey),
and [storage quotas and eviction](https://developer.mozilla.org/en-US/docs/Web/API/Storage_API/Storage_quotas_and_eviction_criteria).

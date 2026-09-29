# Third-party software / 第三方软件

This is an unofficial community portability wrapper, not endorsed by Tencent. WorkBuddy and other product names and marks belong to their respective owners. The launcher has an independently drawn community icon, not an official Tencent signature or certification.

## Included in this ZIP

- Generic launcher components adapted from **DSH Portable 1.0.4**, MIT. Its copyright notice is retained in `LICENSE`. The original DSH release has not been changed.
- **7-Zip 26.00 x64** — `tools/7z.exe`, `tools/7z.dll`, unmodified. Copyright Igor Pavlov; LGPL, unRAR restriction and additional notices in `tools/License.txt`. Original readme retained. Original binary distribution: https://www.7-zip.org/a/7z2600-x64.exe ; corresponding source: https://www.7-zip.org/a/7z2600-src.7z ; project: https://www.7-zip.org/ . The wrapper uses NSIS/7z extraction, not RAR compression.
- **Go 1.26.1 runtime/standard library**, compiled into the launcher, BSD-style license in `docs/licenses/Go-LICENSE.txt`. Source: https://go.dev/dl/go1.26.1.src.tar.gz .
- **golang.org/x/sys v0.42.0**, **golang.org/x/net v0.51.0**, **golang.org/x/text v0.34.0** — BSD-style licenses in `docs/licenses/`, exact versions/checksums in source/go.mod and source/go.sum. Corresponding sources are published in the Go modules and their upstream repositories.

Full community launcher source, integration assets, tests and resource configuration are included. `github.com/tc-hib/go-winres v0.3.3` was used as a build-time resource generator; community icon artwork is MIT.

## Downloaded on first preparation, not redistributed in this ZIP

- **Tencent WorkBuddy CN Desktop** — https://www.workbuddy.cn/ . Subject to Tencent's original product terms and notices; this wrapper does not grant a new license to Tencent software.
- Its bundled Electron / Chromium / Node.js / Undici 6.25.0 / native modules / other third-party components retain their shipped licenses. Preserve the original application notices with any prepared runtime. Undici is used from that application, not separately bundled here.

The fixed original installer was retrieved from the official HTTPS channel and checked using Authenticode verification; its SHA-256 is pinned in this wrapper. The launcher modifies selected ASAR modules and rebinds embedded ASAR integrity metadata in a staged EXE while leaving integrity fuses enabled. The staged EXE's invalidated original signature is removed. **The launcher and modified runtime are unsigned community derivatives; the original installer's Tencent signature does not authenticate these modifications.** See README and docs for the exact boundary and verification limitations.

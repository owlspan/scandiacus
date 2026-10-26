# Antivirus false positives, code signing and reporting

A program built by `scandiacus build` is a PE file written by this
compiler's own linker, not by MSVC or MinGW. Scanners that score files
on structure and reputation (VirusTotal engines, Defender cloud
protection, SmartScreen) see a new binary with no reputation and no
signature, and a small share of them flag it. Nothing in the output is
malicious; the fix is to make the files look ordinary, sign them, and
report the false positives to the vendors. Hiding or mutating the code
to dodge a scanner is not a fix and is not done here.

## What the compiler already writes

Checked by `TestExecutableLooksOrdinary` in `asm-src/compiler/pe_test.go`:

- Conventional section names, and no section that is both writable and
  executable. Read-only data shares the executable `.text` pages, which
  are read-only.
- ASLR (`DYNAMIC_BASE`, `HIGH_ENTROPY_VA`), DEP (`NX_COMPAT`), a base
  relocation table, and an unwind table (`.pdata`).
- A correct image checksum.
- A version resource (Properties, Details). Set the fields with
  `--company`, `--product`, `--description`, `--copyright` and
  `--file-version`; an empty CompanyName is a mild negative signal, so
  set it for anything you distribute.
- An application manifest with `asInvoker` and a `supportedOS` list for
  Windows Vista through 11.
- Imports only from system DLLs, by name.

Not written, and not planned without a measured need: a debug
directory, a load configuration directory (CFG, SafeSEH), a Rich
header. MSVC output has them; they do not change what a program does.

## Code signing

A signature is the strongest single change. It ties the file to a named
publisher, and SmartScreen and most engines treat signed files from an
established certificate far more kindly.

1. Get a certificate. Options, cheapest first:
   - Azure Trusted Signing (Microsoft's service, a few dollars a month,
     needs an identity check and a business or individual validation).
   - An OV or EV code signing certificate from a certificate authority
     (Sectigo, DigiCert, SSL.com and others). Since 2023 the private key
     must live on a hardware token or in a cloud HSM; you cannot get a
     plain .pfx file any more.
   EV used to give instant SmartScreen reputation; that advantage was
   removed in 2024, so reputation now builds with downloads either way.
2. Sign with `signtool` from the Windows SDK, always with a timestamp so
   the signature outlives the certificate:

       signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 ^
           /a program.exe
       signtool verify /pa /v program.exe

   For Azure Trusted Signing, use `signtool` with the Trusted Signing
   dlib, as Microsoft's documentation describes.
3. The compiler's checksum is computed when it writes the file, and
   `signtool` appends a signature after it, so sign last and do not
   edit the file afterwards.
4. What to sign:
   - `scandiacus-X.Y.Z-setup.exe`, the installer. Sign the installed
     `scandiacus.exe` before the installer is built, so both are signed.
     In Inno Setup, `SignTool=` in `scandiacus.iss` plus `SignedUninstaller`
     does both.
   - Programs users build with `scandiacus build` are theirs to sign.
     There is no way for the compiler to sign them without the user's
     certificate, so the compiler leaves them unsigned. A future
     `--sign` option could call `signtool` for them; it is not added
     here.
5. Release CI cannot hold a hardware-token certificate. Either sign on
   a machine you control and upload the signed asset, or use a cloud
   signing service that CI can call (Azure Trusted Signing has a GitHub
   Action).

## Reporting false positives

Do these yourself, with the files you actually ship. Report the release
files, not copies you rebuilt, because hashes must match what users
download. Say plainly that the file is a program built by an open-source
compiler and give the repository link.

- Microsoft Defender: https://www.microsoft.com/en-us/wdsi/filesubmission
  Choose "Software developer", sign in, submit the file, and select
  "I believe this file is clean". The result usually comes back within
  a few days. Defender detections also have a name (for example
  `Trojan:Win32/Wacatac.B!ml`); put it in the submission.
- VirusTotal: open the file's page, use the Community tab to comment,
  and for each vendor that flags it use that vendor's form below. A
  re-scan after the vendor updates clears the label.
- Other vendors (each has a "report a false positive" page; search the
  name plus "false positive submission"):
  - Kaspersky: https://opentip.kaspersky.com
  - ESET: https://support.eset.com/en/kb141 (sample submission)
  - Avast and AVG (same engine): https://www.avast.com/false-positive-file-form.php
  - Bitdefender: https://www.bitdefender.com/consumer/support/answer/29358/
  - Sophos: https://support.sophos.com/support/s/filesubmission
  - Malwarebytes: https://forums.malwarebytes.com (False Positives section)
  - Symantec / Norton: https://submit.symantec.com/false_positive/
  - CrowdStrike, SentinelOne, Cylance and other ML engines usually
    take reports through a vendor contact or through the AV-Comparatives
    / VirusTotal vendor channels; a signed binary makes these go away
    more reliably than a report does.
  Links change; if one is dead, find the vendor's current page.
- Microsoft SmartScreen ("Windows protected your PC"): submit the file
  to the Defender form above, or use "Report this app" from the
  SmartScreen dialog. Reputation rises as a signed file is downloaded.

Keep a note of each submission (vendor, date, file SHA-256, detection
name) so the same report is not sent twice. Do not upload a binary to
VirusTotal for the first time without checking the detection history
first: an upload is public to vendors and cannot be withdrawn.

## If one engine keeps flagging

Ask it for the detection name, then check whether the trigger is
something the compiler does for a reason (for example installing a
vectored exception handler or reserving a large stack). If a structural
change would make the file closer to what a normal linker writes at no
cost, it belongs in `pe.go` with a test. If it is a reputation score,
signing and reporting are the remedies.

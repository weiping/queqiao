# Signing the macOS app

The release workflow signs and notarises `queqiao.app` when these five
secrets are set on the repository; without them it ships an unsigned zip
whose notes carry the one-line Gatekeeper pass. Everything below is a
one-time setup.

## What you need (an Apple Developer account)

1. **A Developer ID Application certificate**, exported as `.p12` with a
   password. Check for one first:

   ```sh
   security find-identity -v -p codesigning | grep "Developer ID Application"
   ```

   If none: developer.apple.com → Account → Certificates → ➕ → *Developer
   ID Application* (a CSR is required; the site shows the command). Import
   the downloaded `.cer` into Keychain Access, then export the
   cert+private key pair as `.p12`.

2. **An App Store Connect API key** for notarisation: appstoreconnect.apple.com
   → Users and Access → Integrations → App Store Connect API → generate a
   key with **Developer** access. Download the `.p8` (offered once) and note
   the **Key ID** and **Issuer ID**.

3. **The Team ID** (10 characters, on the membership page).

## Setting the secrets

```sh
base64 -i Certificate.p12  | gh secret set APPLE_CERT_P12
gh secret set APPLE_CERT_PASSWORD        # the .p12 export password
base64 -i AuthKey_XXXX.p8 | gh secret set APPLE_API_KEY
gh secret set APPLE_API_KEY_ID           # the .p8's Key ID
gh secret set APPLE_API_ISSUER           # the Issuer ID
gh secret set APPLE_TEAM_ID
```

## What the workflow does with them

`app` job in `.github/workflows/queqiao-release.yml`: builds `queqiao.app`
on a macOS runner, imports the certificate into a temporary keychain,
`codesign --deep --options runtime --timestamp`, zips with `ditto
--keepParent`, submits with `notarytool --wait`, staples, rezips the
stapled app and uploads `queqiao-app-macos.zip` to the `qq-v*` release.

A stapled app passes Gatekeeper offline on first open — the same
experience as upstream magpie's, which does this from its separate
yetone/magpie-releases repository; here the secrets stay in this repo.

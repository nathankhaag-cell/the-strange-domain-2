# Installing The Strange Domain

There are two parts:

- **The Domain Node** is the server. It runs on one computer that stays on: a home PC, a Mac, a Raspberry Pi or a cloud server. Everyone's apps connect to it.
- **The apps** are what people use: a desktop app for Windows, macOS and Linux, an Android app, or a web browser.

Everything is downloaded from the project's Releases page:
https://github.com/nathankhaag-cell/the-strange-domain-2/releases/latest

## Making a new release

GitHub builds every download for you. Nothing needs to be compiled by hand. Either:

- **From the website:** open the repository on GitHub, click **Actions**, pick **Release** on the left, click **Run workflow**, type a version number such as `0.1.0`, and click the green **Run workflow** button. Or:
- **From a terminal:** tag the commit and push the tag:

  ```sh
  git tag v0.1.0
  git push origin v0.1.0
  ```

Version numbers look like `1.2.3`. A version with a dash, such as `0.2.0-beta.1`, is published as a pre-release.

The build takes about 20 to 30 minutes. When it finishes, the new release appears on the Releases page with every file listed below and a `SHA256SUMS` file of checksums. If something fails, the Actions tab shows which part and why.

## What to download

| You have | Download |
| --- | --- |
| Windows PC | `The-Strange-Domain-<version>-windows-x64-setup.exe` |
| Mac with Apple silicon (M1 or later) | `The-Strange-Domain-<version>-macos-arm64.dmg` |
| Mac with an Intel processor | `The-Strange-Domain-<version>-macos-x64.dmg` |
| Linux PC (Debian, Ubuntu, Mint) | `The-Strange-Domain-<version>-linux-amd64.deb` |
| Linux PC (any other) | `The-Strange-Domain-<version>-linux-x86_64.AppImage` |
| Raspberry Pi 4 or 5 with a desktop (64-bit) | `The-Strange-Domain-<version>-linux-arm64.deb` |
| Android phone or tablet | `The-Strange-Domain-<version>-android.apk` (or `...-android-debug-signed.apk`, see below) |

Not sure which Mac you have? Apple menu > **About This Mac**. "Chip: Apple M..." means Apple silicon; "Processor: ... Intel" means Intel.

For the server, download the `domain-node-<version>-...` archive for the computer that will run it (see [Running the Domain Node](#running-the-domain-node)).

## Installing the apps

The apps are not signed with paid Apple, Microsoft or Google developer certificates yet, so each system warns you the first time. This is expected.

### Windows

1. Run the `...-setup.exe` file.
2. If a blue box says **Windows protected your PC**, click **More info**, then **Run anyway**.
3. Follow the installer. The app appears in the Start menu as **The Strange Domain**.

### macOS

1. Open the `.dmg` file and drag **The Strange Domain** into **Applications**.
2. Open it from Applications. macOS says it cannot check the app for malicious software, or that it was not opened. Click **Done** (or **OK**).
3. Open **System Settings** > **Privacy & Security**, scroll down to the message about The Strange Domain, and click **Open Anyway**. Enter your password if asked, then click **Open Anyway** again.

On macOS 14 (Sonoma) and earlier you can instead Control-click (or right-click) the app in Applications, choose **Open**, and then click **Open**.

You only need to do this once.

### Linux

- **.deb** (Debian, Ubuntu, Mint, Raspberry Pi OS): double-click it to open it in your software installer, or run
  `sudo apt install ./The-Strange-Domain-<version>-linux-amd64.deb`.
  The app appears in your applications menu.
- **AppImage** (any distribution): right-click the file > **Properties** > **Permissions** > tick **Allow executing file as program**, then double-click it. Or in a terminal: `chmod +x The-Strange-Domain-*.AppImage` and run it. If it does not start on Ubuntu 24.04 or later, use the .deb instead.

### Android

1. On the phone, open the release page in the browser and tap the `.apk` file to download it.
2. Open the downloaded file. Android says your phone is not allowed to install unknown apps from this source. Tap **Settings**, turn on **Allow from this source**, and go back.
3. Tap **Install**. If Google Play Protect warns about an unrecognised app, tap **More details** > **Install anyway**.

**About `...-android-debug-signed.apk`:** until an Android signing key is set up (see [Signing](#signing-optional)), releases contain a debug-signed app. Each one has a different signature, so installing a newer one means uninstalling the old one first, and that deletes the keys and messages stored in the app. After reinstalling, link the phone again from another device (Devices > link code), or use your recovery code.

## First start: connecting to your node

The first time you open the desktop or Android app, it asks for the **node address**. Type the address of the computer running the Domain Node, for example `192.168.1.20:8743`. If you leave out the port, 8743 is used. Then sign in as usual.

- **Desktop app:** to switch to another node later, use **File > Change node**.
- **Android app:** sign out, then tap **Change node** on the sign-in screen. Each node keeps its own keys and messages in the app.

To find the node computer's address: on a Raspberry Pi or Linux, run `hostname -I`; on Windows, run `ipconfig` and look for "IPv4 Address"; on a Mac, System Settings > Wi-Fi > Details.

## Running the Domain Node

### On a Windows PC or a Mac

1. Download and unpack `domain-node-<version>-windows-x64.zip`, `...-macos-apple-silicon.tar.gz` or `...-macos-intel.tar.gz`.
2. Run `domain-node` (on Windows, double-click `domain-node.exe`; allow it through the firewall when asked). On a Mac, the first time, Control-click it > **Open**, or use **Open Anyway** in Privacy & Security as above.
3. Leave the window open. On that same computer you can use it in a browser at http://localhost:8743.

The first account created on a new node becomes its administrator.

### On a Raspberry Pi (runs all the time, starts by itself)

1. Find out which version your Pi runs: `uname -m`. `aarch64` means 64-bit: download `domain-node-<version>-linux-arm64-raspberrypi.tar.gz`. `armv7l` means 32-bit: download `...-linux-armv7-raspberrypi.tar.gz`. (On a Linux PC use `...-linux-x64.tar.gz`.)
2. Unpack it and install the program and the service:

   ```sh
   tar -xzf domain-node-*-linux-*.tar.gz
   cd domain-node-*/
   sudo cp domain-node /usr/local/bin/domain-node
   sudo useradd --system --home /var/lib/strange-domain strange-domain
   sudo cp domain-node.service /etc/systemd/system/
   sudo systemctl enable --now domain-node
   ```

3. Check it is running: `systemctl status domain-node`. Its log: `journalctl -u domain-node`.

To update later, download the new archive, copy the new `domain-node` over `/usr/local/bin/domain-node`, and run `sudo systemctl restart domain-node`. Your data in `/var/lib/strange-domain` is kept.

## Opening the node in a browser on another computer (HTTPS)

Browsers only allow the encryption this client needs on secure pages: `https://` addresses, or `http://localhost` on the node's own computer. A page at `http://192.168.1.20:8743` opened from another computer shows a message saying so. The desktop and Android apps do not have this problem and work with plain `http://`.

To use a browser from other computers, turn on HTTPS on the node:

- **Simplest: a self-signed certificate.** Start the node with `-tls-self-signed`. On a Pi, edit the service (`sudo systemctl edit --full domain-node`), change the `ExecStart` line to

  ```
  ExecStart=/usr/local/bin/domain-node -listen :8743 -data /var/lib/strange-domain -tls-self-signed
  ```

  and run `sudo systemctl restart domain-node`. The node makes a certificate once and keeps it in its data folder (`tls/` inside it). Its log shows the certificate's **fingerprint** (`journalctl -u domain-node | grep sha256`).

  Then open `https://192.168.1.20:8743`. The browser warns that the connection is not private, because nobody vouched for the certificate. Check the fingerprint first (Chrome and Edge: click the warning icon in the address bar > certificate details > SHA-256 fingerprint) and, if it matches the node's log, click **Advanced** > **Proceed**.

  The desktop app shows the same fingerprint and asks you to confirm it once.

- **If you have a real certificate** (for example from Let's Encrypt for a domain name), start the node with `-tls-cert /path/to/fullchain.pem -tls-key /path/to/privkey.pem`. Browsers then show no warning.

The Android app cannot use a self-signed certificate (only one the phone already trusts). To serve both, add `-tls-listen :8744`: the node then keeps plain HTTP on 8743 for the apps and serves HTTPS on 8744 for browsers:

```
ExecStart=/usr/local/bin/domain-node -listen :8743 -tls-listen :8744 -data /var/lib/strange-domain -tls-self-signed
```

Browsers then use `https://192.168.1.20:8744`, and the apps keep using `192.168.1.20:8743`.

On plain `http://`, messages are still end-to-end encrypted, but someone else on the same network could see sign-in tokens. HTTPS prevents that.

## Signing (optional)

Signing removes the warnings above and, for Android, lets new versions install over old ones. The release workflow signs automatically once these repository secrets exist (GitHub > Settings > Secrets and variables > Actions > New repository secret). Without them, everything still builds, unsigned.

**Android (recommended before people rely on the app).** Make a signing key once, on any computer with Java:

```sh
keytool -genkeypair -v -keystore strange-domain.keystore -alias strange-domain \
  -keyalg RSA -keysize 4096 -validity 10000
base64 -w0 strange-domain.keystore > keystore.txt   # on a Mac: base64 -i strange-domain.keystore -o keystore.txt
```

Add the secrets `ANDROID_KEYSTORE_BASE64` (the contents of keystore.txt), `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS` (`strange-domain`) and, if different from the keystore password, `ANDROID_KEY_PASSWORD`. Keep the keystore file and passwords somewhere safe: if they are lost, everyone has to uninstall and reinstall the app.

**macOS** (needs a paid Apple Developer account): `MAC_CSC_LINK` (Developer ID Application certificate as a base64 .p12) and `MAC_CSC_KEY_PASSWORD`; for notarization also `APPLE_API_KEY_BASE64` (the .p8 key, base64), `APPLE_API_KEY_ID` and `APPLE_API_ISSUER`.

**Windows** (needs a code-signing certificate): `WIN_CSC_LINK` (base64 .pfx) and `WIN_CSC_KEY_PASSWORD`.

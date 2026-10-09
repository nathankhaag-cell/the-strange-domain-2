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

### Seeing what the node is doing

When it starts, the node prints its version, its data folder and every address it can be opened at, for example:

```
The Strange Domain node v0.1.0
Data folder: /var/lib/strange-domain
Open in a browser: http://localhost:8743
Open in a browser: http://192.168.1.20:8743
```

With a self-signed certificate it also prints the certificate's fingerprint. On a Pi, `journalctl -u domain-node` shows the same lines.

Every minute it logs one `status` line: devices and live connections, users, domains, requests per minute, messages relayed, data in and out, and errors. `-status-interval 10s` changes how often; `-status-interval 0` turns it off. `-log-level debug` also logs every request (without IDs or invite codes in the paths).

In the app, the node's administrator (the first account created on it) has a **Node admin** button at the top. It shows the same numbers live, plus uptime, version, database size, per-domain counts and recent errors. Nobody else can open it. It never shows message contents (the node cannot read them) or anyone's network address.

## Updates

- **Node:** at start-up and once a day, the node asks GitHub whether a newer release exists. If one does, it logs `a newer version of the node is available` with the download link, and the Node admin view shows it. It never downloads or installs anything; update it as described above. Without internet (a home network with no internet, a mesh network) the check fails quietly and the node works as usual. `-update-check=false` turns it off.
- **Desktop app:** a few seconds after it starts, the app checks GitHub. If a newer version exists it asks first. On Windows and with the AppImage on Linux, **Update and restart** downloads and installs it and reopens the app. With the .deb and on macOS the app cannot replace itself, so it offers **Open download page** instead. Set the environment variable `STRANGE_DOMAIN_NO_UPDATE_CHECK=1` to turn the check off.
- **Android app:** when it starts, the app checks GitHub and, if there is a newer version, shows a bar with a **Download** link to the new `.apk`. Android does not let an app installed outside the Play Store update itself, so you install the download as in [Android](#android) above. A debug-signed app must be uninstalled first (see the note there).

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

## Attachments and storage

Files people send (pictures, PDFs and so on) are encrypted on their device; the node only keeps the encrypted copies, in `blobs/` inside its data folder. Two options control how much space they may use:

- `-max-upload-mb 25`: the largest file anyone can send (default 25 MB).
- `-upload-quota-mb 1024`: how much each person may keep on the node in total (default 1024 MB).

On a Raspberry Pi with a small SD card, lower both, for example `-max-upload-mb 10 -upload-quota-mb 200`. Deleting a message deletes its files; uploads that never got sent are removed after an hour.

## Voice and video calls

People can talk in a domain's **Voice Relays** (Join, Leave, Mute, push to talk, Camera, Share screen) and call each other in **Confessions** and **Conclaves** (Call or Video call; the others hear a ringing sound and see Answer and Decline).

Calls are end-to-end encrypted. Every sound and picture is encrypted on the speaker's device with a key that comes from the chat's MLS group, the same encryption messages use. The node passes the encrypted media on to the others in the call; it cannot hear or see it.

### Ports and firewalls

The node carries call media over **UDP port 8745** (in addition to TCP 8743 for the app itself).

- **On a home network or mesh network with no internet:** nothing else is needed, as long as the computer's firewall lets UDP 8745 in.
  - Windows: the first time the node starts, allow it on **Private networks** when Windows asks. To add the rule by hand: `netsh advfirewall firewall add rule name="Strange Domain calls" dir=in action=allow protocol=UDP localport=8745`.
  - macOS: allow incoming connections for `domain-node` when asked (System Settings > Network > Firewall).
  - Raspberry Pi / Linux with ufw: `sudo ufw allow 8743/tcp` and `sudo ufw allow 8745/udp`.
- **Reaching the node from the internet** (the node at home, people calling in from elsewhere): on your router, forward **TCP 8743** (or your HTTPS port) and **UDP 8745** to the node computer, and tell the node your public IP address so it can offer it to callers:

  ```
  ExecStart=/usr/local/bin/domain-node -listen :8743 -data /var/lib/strange-domain -rtc-public-ip 203.0.113.7
  ```

  Use your router's public address in place of `203.0.113.7` (a search for "what is my IP" shows it). People on the home network keep using the local address. If your public address changes from time to time, update the option when it does.
- **A cloud server:** open TCP 8743 and UDP 8745 in its firewall or security group. If the server only knows its private address (most clouds), add `-rtc-public-ip` with its public address.

Options:

| Option | What it does |
| --- | --- |
| `-rtc-udp-port 8745` | The UDP port for all call media. |
| `-rtc-udp-port 0 -rtc-port-range 50000-50199` | Use a range of UDP ports instead, one per person in a call. |
| `-rtc-tcp-port 8746` | Also carry call media over TCP on this port, for networks that block UDP (off by default). Forward or open it like the UDP port. |
| `-rtc-public-ip <address>` | Public address(es), comma-separated, offered to callers as well as the computer's own addresses. |
| `-rtc-video=false` | Voice only: no cameras or screen sharing. Use it on slow links (mesh networks, a Pi on Wi-Fi). |
| `-rtc=false` | Turn calls off. |

No STUN or TURN server is needed: everyone in a call connects to the node itself, so if they can reach the node's UDP (or TCP) call port, the call works. The node therefore has no TURN server built in.

### Bandwidth

Calls are tuned for a Raspberry Pi and slow links: voice uses about 32 kbit/s per speaker (plus about 12 kbit/s of encryption overhead), cameras at most 640x360 at 15 frames per second (about 500 kbit/s), and screen sharing at most 1280x720 at 5 frames per second (about 800 kbit/s). The node forwards each person's media to everyone else in the call, so its upload is roughly what each person sends times the number of other people. A Pi 4 handles a family-sized voice call easily. The **Node admin** view shows how many calls are running, how many people are in them and the media traffic.

### What works where

- **Desktop app, Chrome, Edge, and the Android app:** everything. Firefox and Safari use a different (standard) way to encrypt call media, which this client supports but which has had less testing.
- A browser that cannot encrypt call media cannot join calls; it says so instead of joining without encryption.
- A browser on another computer needs HTTPS (see above) for the microphone and camera, as it does for the rest of the client.
- **Android:** the phone asks for the microphone (and the camera, for video) the first time a call needs it. Calls only continue while the app is open: Android pauses the app soon after it goes to the background or the screen turns off, which ends the call, and an incoming call only rings while the app is open.
- People muted in a domain can join its Voice Relays to listen but cannot be heard. Roles without the Voice Relay permission (Postulants by default) cannot join.

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

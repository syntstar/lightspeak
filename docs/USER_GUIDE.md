# Lightspeak user guide

Lightspeak is a lightweight app for voice channels, chat, files and screen sharing. There is a Windows app (Windows 10/11) and a browser client for everything else. Each group runs its own server, so you always start with an invite link from someone who is already on it.

## 1. Install

1. Open the **invite link** you received (`https://…/invite/…`).
2. Click **“Download Lightspeak for Windows”** and run `Lightspeak-setup.exe`. No admin rights needed.
3. Windows may show **“Windows protected your PC”** (SmartScreen). That is because the installer is not commercially code-signed: click **“More info”**, then **“Run anyway”**.

### No install: in the browser

On the invite page click **“Open in browser”** – Lightspeak then runs in Chrome, Edge, Firefox or Safari (current version), also on a Mac. Create your account as described below and allow the microphone when you first join. Differences to the app: **push-to-talk and shortcuts only work while the Lightspeak tab is in front**, notifications after you allow them under **Settings → Notifications**, no tray icon or autostart. When sharing your screen, tick **“Share tab audio”** or **“Share system audio”** in the browser's dialog so sound goes along. Calls are end-to-end encrypted just the same; the key lives in the browser – if you clear the site data, simply sign in again.

## 2. Create your account

1. Back on the invite page, click **“Open in Lightspeak”** – or paste the invite link into the app on first start.
2. The app shows the server's name. If it looks right, continue.
3. Choose a **username** (3–32 characters: a–z, 0–9, dot, underscore) and a **password** (at least 10 characters) – done. You land directly in the space you were invited to.

Forgot your password? The server admin can set a temporary one; change it afterwards under **Settings → My account**. There you can also **delete** your account (with your password; hand over or delete your own spaces first). Your messages then remain as “Deleted user”.

## 3. Talk

- Click a **voice channel** (speaker icon) to join. The voice panel with **Disconnect** appears at the bottom left.
- **Microphone** and **headphones** (mute/deafen) are at the bottom left next to your name.
- Calls are **end-to-end encrypted** (lock in the voice panel) – not even the server can listen in.
- Call a friend directly: in a direct message, click **“Start call”**.
- **Special voice channels:** On a **stage** (microphone icon) most people listen and only certain roles may speak – the voice panel then says “You are listening”. In the **away channel** (moon) all microphones stay off – depending on the setting, anyone who neither talks nor uses the computer for a while is moved there automatically. A **lock** means: you can’t join this channel. A **crossed-out eye** means: private channel – only certain roles can see it.

### Push-to-talk

The default is **voice activation** (the microphone turns on when you speak). For push-to-talk:
**Settings** (gear at the bottom left) → **Voice & audio** → **Input mode: Push-to-talk** → click **Push-to-talk key** and press the key you want (mouse buttons 4/5 and combinations work too).

- Works in full-screen games as well.
- If a game runs **as administrator**, Windows does not pass its keys to Lightspeak – a notice appears. Fix: start the game normally.
- The **microphone test** and **noise suppression** are on the same page.

## 4. Share your screen

1. In a voice channel, click **“Share screen”**. A picker with previews of all windows and screens opens.
2. At the top choose **Window** (one app – only its sound is sent) or **Entire screen** (all system sound, without the voices from Lightspeak), click the one you want → **“Share”**.
3. Lightspeak now shows a **preview** of what the others will see – nothing is sent yet. Picked the wrong one? **“Change …”**.
4. Pick quality and frame rate (**Content: Game & video** for motion, **Text & code** for sharpness) → **“Go live”**.
5. **Switch while streaming:** click the arrows icon **“Change source”** in the voice panel and pick another window or screen – the stream continues without interruption and the sound follows. The gear next to it changes quality etc., **“Stop streaming”** ends it.

To watch: click the **LIVE** badge next to the name. No sound when sharing a window? Pick the right app under **“Sound from: …”** in the voice panel.

## 5. Chat & files

- Messages support **bold**, *italic*, `code`, links and @mentions. Right-click a message to edit, delete, reply or pin it.
- Files up to 200 MB (default, the server may set another limit) via drag & drop, **Ctrl+V** (screenshots) or **+** next to the message box.
- **Ctrl+K** opens the quick switcher for channels and people, **Ctrl+F** the search.
- **Announcement channels** (megaphone icon): everyone reads along, only certain roles post. Where files or reactions are switched off, their buttons are missing. In **slow mode** the input shows when you can write again.
- Add friends: **Direct messages** at the top left → **“Add a friend”** → username.

## 6. Invite friends

If you may invite people (owner, admin or a role with “Invite people”): click the **space name** at the top left → **“Invite people”** → pick how long it is valid → **“Create and copy link”** → send the link.

## 7. Good to know

- **Closing** the window keeps Lightspeak running in the notification area (next to the clock) so you stay reachable. Quit by right-clicking its icon. Can be turned off under **Settings → General**.
- **Updates** arrive automatically from the project’s GitHub releases: new versions are downloaded in the background and installed the next time you start the app. A banner lets you update right away. Every update is signed.
- **Light/dark**, accent colour, text size, language (German/English): **Settings → Appearance**.
- Problems? **Settings → About → Diagnostics → Export** saves a diagnostic report as a text file (no passwords or keys) you can send to the admin.

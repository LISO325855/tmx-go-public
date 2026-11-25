# Pair your phone

A paired browser lets you use tmx and answer owner approvals from your phone.
It uses your owner PIN, not a passkey. Pair only a browser you control.

## Connect a phone: one scan

At the computer, set up tmx using the [installation guide](INSTALL.md). Then:

1. Open **Settings → Owner devices** and click **Connect a phone** (or press
   Ctrl/⌘ K and type "connect a phone").
2. Follow the steps it shows. It only shows what is still missing, one step
   per screen, and notices by itself when a step is done:
   - choose your owner PIN (6–12 digits) if you have none yet;
   - install Tailscale on the computer and sign in;
   - allow secure connections for your Tailscale account (one click on the
     page it opens);
   - install Tailscale on the phone, sign in with the same account and switch
     it on.
3. A code appears. Point the phone's camera at it and open the link.
4. On the phone, type your owner PIN and tap **Connect**. Done.

This works at home and away: the phone reaches the computer through Tailscale
wherever it has internet. tmx sets the computer's side up itself: it serves
itself on the computer's Tailscale name over HTTPS and adds that name to
`api.trusted_hosts` (it asks you to confirm that once). If another program
already uses the name's port 443, tmx takes 8443 or 10000.

The code works once, for ten minutes, and only together with your PIN. Closing
the dialog cancels it. Incorrect PIN attempts can cause a temporary wait; follow
the message on screen. The phone receives its own browser session, so use the
same browser after pairing (the one the camera opened). Clearing its cookies or
using a different browser requires pairing again. An ordinary device session
expires after 90 days.

### When the phone says "Can't reach your computer"

Check, in this order: the phone has internet; the Tailscale app on the phone is
switched on; the computer is on, awake and running tmx. tmx keeps trying by
itself and opens as soon as the computer answers.

If the camera's link shows the browser's own error page instead of tmx, the
phone is not on Tailscale yet: open the Tailscale app, switch it on, scan again.

## If Tailscale does not work on your phone: the same Wi-Fi

This is a fallback for home, off unless you switch it on. In the **Connect a
phone** dialog choose **Tailscale doesn't work on the phone? Use the same
Wi-Fi**, then **Switch on** (or use the switch under **Settings → Owner
devices**). On Windows, answer **Yes** once when Windows asks to let tmx
through the firewall. Then scan the code and type your owner PIN, as above.

What to know before you use it:

- It only works while the phone is on the same Wi-Fi (or network) as the
  computer. Away from home the phone cannot reach tmx this way.
- tmx then also answers on your home network over plain http, without the
  secure connection. A device needs the code on your screen and your owner
  PIN to connect, and no token is accepted over the network.
- Without the secure connection the phone's browser allows no dictation, no
  notifications, no fingerprint unlock and no "install as app". Those need
  the Tailscale way.
- If your computer's address on the network changes, connect the phone again.
- Switching it off closes it at once and disconnects the phones that were
  connected this way. Phones connected through Tailscale are not affected.

## The lock on your phone

tmx on a paired phone or tablet locks itself when you put the phone away: five
minutes after you leave the page (you can change that), and after every restart
of tmx on the computer. To open it again, use your fingerprint or Face ID, or
type your owner PIN. While it is locked the computer answers nothing to that
phone, so nothing of tmx can be read on it.

- **Fingerprint / Face ID:** the phone offers it once after you connect (or
  after the first time you unlock with the PIN). You can add or remove it later
  on the phone under **Settings â†’ Owner devices**. It only opens the lock:
  approvals still ask for your owner PIN.
- **The setting:** on the computer, **Settings â†’ Owner devices â†’ Lock tmx on my
  phones**: on or off, and "Lock after" (immediately, 30 seconds, 1, 5 or 15
  minutes). It can only be changed at the computer.
- **Wrong PINs** make you wait a little longer each time. They never disconnect
  the phone, so you cannot lock yourself out while you are away.
- Only phones and tablets lock. A browser on another computer that you paired
  stays open, and the computer running tmx is never locked.
- **Lock now** is on the phone under **Settings â†’ Owner devices**.

## Pair by opening the address yourself

If the HTTPS address is already set up you can also open it in the phone's
browser without a code:

1. Open your installation's trusted HTTPS address in your phone's browser.
2. On **Pair this device**, enter a recognizable device name and tap
   **Pair this device**. Keep that page open.
3. Enter your owner PIN and tap **Pair with my PIN**.
4. Wait for **Device paired**, then tap **Continue**.

The request expires in two minutes. If it expires, start again in the same
browser.

## Approve the code at the host instead

If you prefer, leave the phone's pairing page open and read its displayed code.
On the host computer, open **Settings → Owner devices**, enter that code and
choose **Approve**. Complete the host's authentication prompt. The phone then
shows **Device paired**; tap **Continue**. Approve only the code visible on your
own phone. An already-paired remote browser cannot approve new device codes.

The QR invites in **Chats** connect chat contacts; they are a separate flow from
pairing an owner browser.

## Approvals and removing access

When an action needs owner confirmation, the phone asks for your owner PIN.
Some actions require a fresh confirmation even during an existing presence
session. Host safety switches and grace settings remain host-only.

To remove a browser's access, open **Settings → Owner devices**, find the device
and choose **Revoke**, then complete the requested owner confirmation. Revocation
ends that device's session and active connections and survives a daemon restart.

If you forgot the PIN, reset it from the host's local **Owner PIN** controls.
Do not share PINs, pairing codes or session links in chats, logs or issue reports.
If the page says **Restricted recovery**, it is not an ordinary paired session:
follow its displayed recovery instructions. Restoring normal owner access
requires fresh host authentication.
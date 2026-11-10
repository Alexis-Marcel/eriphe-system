# Connect Google (Gmail, Google Calendar, Google Drive) with your own Google app

Eriphe reaches Google with **your own Google app**: an OAuth client you create once in your own
Google Cloud project, like Home Assistant's Google integrations. Nobody else holds its key, and no
Eriphe server is in the path: Google sends your browser straight back to your workspace. It takes a
few minutes, **once for every service and every Google account**. Its page in Eriphe (Settings →
Tools → Add → Google → "Create my app", at `/settings/apps/google`) shows these same steps, with
your workspace's exact redirect URI and the scopes, each with a Copy button.

Then each Google account is **one sign-in**: tick the services Eriphe may use (Gmail, Google
Calendar, Google Drive), sign in at Google, and Eriphe asks only for those services' access. A
service added later to the same account asks for that one only; the others stay.

Google's console changes; what follows is its English interface (Google Auth Platform) as of
October 2026.

## Before you start: an address Google takes

Google only sends back to an **authorized redirect URI** that follows its rules
([Redirect URI validation rules](https://developers.google.com/identity/protocols/oauth2/web-server#uri-validation)):

- **https** (plain http only for `localhost`);
- **no raw IP address** (loopback ones aside): `https://192.168.1.2:8443` is refused;
- a host whose **top-level domain is on the public suffix list**: `eriphe.local`, `nas.lan`,
  `*.home.arpa` or a bare name are refused.

Eriphe's redirect URI is `<the address you open Eriphe at>/oauth/callback`. If you open Eriphe at a
local address, the app page says so and offers:

1. **Remote access** (Settings → Access): Tailscale gives Eriphe an
   `https://<name>.<tailnet>.ts.net` address with a real certificate, which Google takes. Open the
   app page at that address and follow the steps from there.
2. **Your own domain**: put Eriphe behind your reverse proxy at, for instance,
   `https://eriphe.example.org`, and set it as the workspace address ([reverse proxy
   guide](reverse-proxy.md)): the redirect URI is then `https://eriphe.example.org/oauth/callback`.
3. **No Google app at all**: Gmail with an app password (Settings → Tools → Mail → Gmail with an
   app password; it needs 2-Step Verification), and for Drive, "Google Drive for desktop" on an
   always-on computer where Eriphe is installed, its synced folder given as a folder of that
   computer (Settings → Folders). Google Calendar has no way without the app.

## The steps

1. **Create a project**: <https://console.cloud.google.com/projectcreate> → name it `Eriphe` →
   **Create**. Make sure it is the selected project for the next steps.
2. **Enable the APIs**, the three at once:
   <https://console.cloud.google.com/flows/enableapi?apiid=gmail.googleapis.com,caldav.googleapis.com,drive.googleapis.com>
   → pick the project → **Enable** (Gmail API, CalDAV API, Google Drive API).
3. **Branding**: Google Auth Platform (<https://console.cloud.google.com/auth/overview>) →
   **Get started**: App name `Eriphe`, your address as **User support email** → Next; Audience
   **External** → Next; your address under **Contact Information** → Next; accept the Google API
   Services User Data Policy → **Create**.
4. **Audience** (<https://console.cloud.google.com/auth/audience>): under **Test users**,
   **Add users**, add each Google address Eriphe will use → **Save**. Or publish the app
   instead (see [Testing or In production](#testing-or-in-production)).
5. **Data access** (<https://console.cloud.google.com/auth/scopes>): **Add or remove scopes** →
   under **Manually add scopes**, paste
   `https://mail.google.com/,https://www.googleapis.com/auth/calendar,https://www.googleapis.com/auth/drive,https://www.googleapis.com/auth/drive.readonly`
   → **Add to table** → **Update** → **Save**.
6. **Clients** (<https://console.cloud.google.com/auth/clients/create>): **Create client** →
   Application type **Web application**, Name `Eriphe`; under **Authorized redirect URIs**, **Add
   URI** and paste exactly the URI the app page shows, e.g.
   `https://eriphe.tail1234.ts.net/oauth/callback` (no final slash) → **Create**. No "Authorized
   JavaScript origins" are needed.
7. **Keep the client**: Google shows the client ID and secret once
   ([Manage OAuth clients](https://support.google.com/cloud/answer/15549257): the secret is hashed
   afterwards, only its last four characters stay visible). Click **Download JSON**, or copy both.
8. **In Eriphe**, on the app page: give the downloaded `client_secret_….json`, or paste the
   client ID and secret → **Save**. The secret goes to the vault (`app.google.client`) and is
   never shown again; "Replace" takes another one.
9. **Connect an account**: Settings → Tools → Add → Google (or Gmail, Google Calendar, Google
   Drive) opens the connect page: tick the tools and "Allow Eriphe to write" if Eriphe may send,
   add events or drop results (always in an `Eriphe` subfolder of the places you allow) → **Sign
   in**. "Google hasn't verified this app" is your
   own app: **Advanced** → **Go to Eriphe**, and allow what is asked (Google may show a box per
   service: one left unticked is said back in the workspace).
10. **Drive: choose folders**: in Settings → Tools, the Drive row's **Places**, once the Files
    server runs: open the folders and give each one "read" or "read + drop".
11. **Another account**: the connect page again, with its own tools. Each account is a card in
    Settings → Tools, with "Add to this account", "Reconnect" and "Remove this account"; every
    account's mailbox is an account of the one `mail` tool, its calendar of `agenda`, its Drive
    folders in the one Files tool.

A Desktop client ("installed" in the JSON) is refused: Google sends it only to loopback addresses,
which a workspace on another machine cannot use. If the uploaded JSON lists redirect URIs and not
this workspace's, the page says so.

## Test the connection

**Test** on the app page checks, without going through Google's consent:

- the workspace's address: one Google takes as a redirect;
- the client ID and secret: a code exchange with a made-up code. Google checks the client before
  the code: `invalid_client` means an unknown client or a wrong secret, `invalid_grant` means
  Google knows the client (and refuses the made-up code, as expected);
- the redirect URI: not asked of Google; the page shows the exact URI the client must list in
  the console (**Clients** → your client → **Authorized redirect URIs**);
- each account connected: its stored authorization, refreshed. An `invalid_grant` there is an
  authorization Google withdrew (the 7 days of Testing, or revoked): the account is marked to
  reconnect, and its card in Settings → Tools has **Reconnect**.

## Testing or In production

To check in the console (Audience → Publishing status), as Google words it today:

- **Testing**: only the test users you listed (up to 100) can connect, and "Authorizations by a
  test user will expire seven days from the time of consent", refresh token included
  ([Manage App Audience](https://support.google.com/cloud/answer/15549945),
  [Refresh token expiration](https://developers.google.com/identity/protocols/oauth2#expiration)):
  the account must then be connected again every week. Eriphe checks each account's
  authorization every half hour: one Google withdrew shows "To reconnect" in Settings → Tools,
  its tools wait (the assistant is told), and **Reconnect** signs in again with the services it
  had, in one click.
- **In production** (**Publish app**): the 7-day expiry of Testing does not apply. The app is not
  verified, so Google shows its "unverified app" warning before the consent, and an unverified
  app is capped at 100 users. For an app for your personal use (fewer than 100 users), Google
  says you can keep using it without verification, clicking through the warning
  ([Unverified apps](https://support.google.com/cloud/answer/7454865),
  [personal use](https://support.google.com/cloud/answer/13464323)).

## Upgrading from the shared app

Versions before this one used a shared Eriphe app behind a sign-in relay on GitHub Pages. That is
gone: `cloud.google_client_file` and `cloud.bounce_url` are still accepted in `core.json`, and
ignored (a line in the log says so). A Drive already connected keeps its token for as long as the
shared app's client exists; to connect again, create your own app as above.

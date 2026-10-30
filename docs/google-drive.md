# Connect Google Drive with your own Google app

Eriphe reaches Google Drive with **your own Google app**: an OAuth client you create once in your
own Google Cloud project, like Home Assistant's Google integrations. Nobody else holds its key, and
no Eriphe server is in the path: Google sends your browser straight back to your workspace. It
takes a few minutes, once. Settings → Tools → Google Drive shows these same steps, with your
workspace's exact redirect URI and a Copy button.

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
local address, the Google Drive page says so and offers:

1. **Remote access** (Settings → Remote access): Tailscale gives Eriphe an
   `https://<name>.<tailnet>.ts.net` address with a real certificate, which Google takes. Open the
   Google Drive page at that address and follow the steps from there.
2. **Your own domain**: put Eriphe behind your reverse proxy at, for instance,
   `https://eriphe.example.org`.
3. **No Google app at all**: install "Google Drive for desktop" on an always-on computer where
   Eriphe is installed, and give its synced Google Drive folder as a folder of that computer
   (Settings → Folders). Eriphe then sees those files like any local folder.

## The steps

1. **Create a project**: <https://console.cloud.google.com/projectcreate> → name it `Eriphe` →
   **Create**. Make sure it is the selected project for the next steps.
2. **Enable the Drive API**: <https://console.cloud.google.com/apis/library/drive.googleapis.com>
   → **Enable**.
3. **Branding**: Google Auth Platform (<https://console.cloud.google.com/auth/overview>) →
   **Get started**: App name `Eriphe`, your address as **User support email** → Next; Audience
   **External** → Next; your address under **Contact Information** → Next; accept the Google API
   Services User Data Policy → **Create**.
4. **Audience** (<https://console.cloud.google.com/auth/audience>): under **Test users**,
   **Add users**, add the Google address whose Drive Eriphe will use → **Save**. Or publish the app
   instead (see [Testing or In production](#testing-or-in-production)).
5. **Data access** (<https://console.cloud.google.com/auth/scopes>): **Add or remove scopes**,
   tick `https://www.googleapis.com/auth/drive` and `https://www.googleapis.com/auth/drive.readonly`
   (filter on "drive") → **Update** → **Save**.
6. **Clients** (<https://console.cloud.google.com/auth/clients/create>): **Create client** →
   Application type **Web application**, Name `Eriphe`; under **Authorized redirect URIs**, **Add
   URI** and paste exactly the URI the Google Drive page shows, e.g.
   `https://eriphe.tail1234.ts.net/oauth/callback` (no final slash) → **Create**. No "Authorized
   JavaScript origins" are needed.
7. **Keep the client**: Google shows the client ID and secret once
   ([Manage OAuth clients](https://support.google.com/cloud/answer/15549257): the secret is hashed
   afterwards, only its last four characters stay visible). Click **Download JSON**, or copy both.
8. **In Eriphe**, Settings → Tools → Google Drive: give the downloaded `client_secret_….json`, or
   paste the client ID and secret → **Save my Google app**. The secret goes to the vault
   (`google-drive-client`) and is never shown again; "Replace my Google app" takes another one.
9. **Connect Google Drive**: tick "Allow Eriphe to drop results" if Eriphe may add files (always in
   an `Eriphe` subfolder of the folders you allow), then Google's consent: "Google hasn't verified
   this app" is your own app: **Advanced** → **Go to Eriphe**, and allow the access to Drive.
10. **Choose folders**: back in the workspace, once the Files server runs, open the folders and
    give each one "read" or "read + drop".

A Desktop client ("installed" in the JSON) is refused: Google sends it only to loopback addresses,
which a workspace on another machine cannot use. If the uploaded JSON lists redirect URIs and not
this workspace's, the page says so.

## Testing or In production

To check in the console (Audience → Publishing status), as Google words it today:

- **Testing**: only the test users you listed (up to 100) can connect, and "Authorizations by a
  test user will expire seven days from the time of consent", refresh token included
  ([Manage App Audience](https://support.google.com/cloud/answer/15549945),
  [Refresh token expiration](https://developers.google.com/identity/protocols/oauth2#expiration)):
  Drive must then be connected again every week.
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

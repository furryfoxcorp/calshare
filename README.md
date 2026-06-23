# calshare

A self-hosted CalDAV server for a small group. It stores events and tasks,
speaks read/write CalDAV to Apple Calendar, Thunderbird, and DAVx5, subscribes
to outside ICS feeds, schedules between members and external guests, and
publishes private "share view" links so friends can follow a filtered slice of
your calendar.

calshare runs as a single static binary behind a reverse proxy. It keeps its
data in one SQLite file.

## What you get

- Full CalDAV for events (VEVENT) and tasks (VTODO), including recurring series
  with overrides and multiple calendars per person.
- Single sign-on for the web UI over OIDC. Per-device app passwords for
  calendar clients.
- Subscriptions to external ICS feeds (Apple public calendars, Google secret
  iCal addresses, Outlook publish URLs, school timetables, and so on), polled
  on a schedule and folded into your calendar list.
- Share views: pick some calendars, choose how much detail to expose (busy
  only, titles, or everything), and hand out a subscribe link. Links can carry
  a password and an expiry, and you can revoke them at any time.
- Invitations between members and to outside email addresses, with replies
  tracked back to the event.

## Requirements

- A host to run the container on.
- A reverse proxy with a TLS certificate (the example uses nginx).
- An OIDC provider for sign-in (Keycloak, Authentik, Auth0, Google, and others
  all work).
- Optional: an SMTP relay to send invitations, and an IMAP mailbox to collect
  replies.

## Install with Docker

The repository ships a Dockerfile and an example compose file under `deploy/`.

1. Register an OIDC client with your provider. Set its redirect URL to
   `https://your-host/oidc/callback`.
2. Copy `deploy/docker-compose.example.yml` to `docker-compose.yml` and fill in
   the values (external URL, OIDC client, admin email, and optionally SMTP and
   IMAP).
3. Point `deploy/nginx/calendar.conf` at your hostname and TLS certificate.
4. Start it:

   ```
   docker compose up -d
   ```

The container runs migrations on startup, then serves on port 8080 behind
nginx.

## Build from source

You need Go 1.26 or newer.

```
go build ./cmd/caldav-share
```

The binary is fully static (the SQLite driver is pure Go), so it runs on a
distroless or scratch base image with no host dependencies. Release archives
for several platforms are built with
[giftwrap](https://github.com/indrora/giftwrap); the config is at
`.github/giftwrap.yml`.

## Commands

- `caldav-share serve` runs the server.
- `caldav-share migrate` applies pending database migrations.
- `caldav-share doctor` checks configuration, database, and bundled time zone
  data.
- `caldav-share admin grant <email>` / `revoke <email>` toggles admin.
- `caldav-share token list` / `revoke <id>` inspects and revokes share links.
- `caldav-share imip drain` sends one pass of the outbound invitation queue.

## Configuration

Settings come from defaults, an optional TOML file (`--config` or
`CALDAV_CONFIG`), environment variables, and flags, in that order of
precedence.

### Core

| Variable | Default | Meaning |
|----------|---------|---------|
| `CALDAV_LISTEN_ADDR` | `:8080` | Address the server listens on |
| `CALDAV_EXTERNAL_URL` | required | Public URL, e.g. `https://vulpes.calshare.fyi` |
| `CALDAV_DB_PATH` | `/var/lib/caldav-share/db.sqlite` | SQLite file path |
| `CALDAV_SESSION_KEY` | generated | Signs session cookies; persisted if unset |
| `CALDAV_DATA_KEY` | generated | Encrypts stored feed credentials; persisted if unset |
| `CALDAV_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `CALDAV_TRUST_PROXY_HEADERS` | `true` | Honor `X-Forwarded-For` |
| `CALDAV_DEFAULT_TZ` | `UTC` | Last-resort time zone |
| `CALDAV_AUTO_MIGRATE` | `true` | Run migrations on startup |

### OIDC

| Variable | Default | Meaning |
|----------|---------|---------|
| `CALDAV_OIDC_ISSUER` | required | Discovery URL of your provider |
| `CALDAV_OIDC_CLIENT_ID` | required | Client id |
| `CALDAV_OIDC_CLIENT_SECRET` | required | Client secret |
| `CALDAV_OIDC_REDIRECT_URL` | `${EXTERNAL_URL}/oidc/callback` | Callback URL |
| `CALDAV_OIDC_SCOPES` | `openid profile email` | Requested scopes |
| `CALDAV_OIDC_ADMIN_EMAILS` | empty | Comma list; these emails become admins on first login |

### Feeds, scheduling, and email

| Variable | Default | Meaning |
|----------|---------|---------|
| `CALDAV_ICS_DEFAULT_POLL_INTERVAL` | `15m` | Default poll interval for feeds |
| `CALDAV_SCHEDULING_ENABLED` | `true` | Enable invitations |
| `CALDAV_SMTP_HOST` | empty | Relay host; without it, external invites are queued but not sent |
| `CALDAV_SMTP_PORT` | `587` | Relay port |
| `CALDAV_SMTP_USER` / `CALDAV_SMTP_PASS` | empty | Relay credentials |
| `CALDAV_SMTP_TLS` | `starttls` | `starttls`, `implicit`, or `none` |
| `CALDAV_IMIP_FROM_OVERRIDE` | empty | Force the From address when the relay requires it |
| `CALDAV_IMIP_REPLY_ADDRESS` | required with SMTP | Address external replies are sent to |
| `CALDAV_IMAP_HOST` | empty | Mailbox host for collecting replies |
| `CALDAV_IMAP_PORT` | `993` | Mailbox port |
| `CALDAV_IMAP_USER` / `CALDAV_IMAP_PASS` | empty | Mailbox credentials |
| `CALDAV_IMAP_TLS` | `implicit` | `implicit`, `starttls`, or `none` |
| `CALDAV_IMAP_FOLDER` | `INBOX` | Folder to poll |
| `CALDAV_IMAP_POLL_INTERVAL` | `60s` | How often to poll |
| `CALDAV_IMAP_PROCESSED_FOLDER` | empty | Move handled messages here instead of marking them read |

## Local development sign-in

OIDC needs a real provider, which is awkward when hacking locally. Setting
`CALDAV_DEV_LOGIN_PASSWORD` (and optionally `CALDAV_DEV_LOGIN_EMAIL`, default
`dev@localhost`) enables a password form on the login page that signs you in as
an admin without OIDC. It is off whenever the variable is unset.

Never set this in production: it bypasses single sign-on. There is no default
password and nothing is hardcoded; the feature exists only when you opt in with
the environment variable.

```
CALDAV_DEV_LOGIN_EMAIL=you@localhost CALDAV_DEV_LOGIN_PASSWORD=somelocalsecret \
  CALDAV_EXTERNAL_URL=http://localhost:8080 \
  CALDAV_OIDC_ISSUER=unused CALDAV_OIDC_CLIENT_ID=unused CALDAV_OIDC_CLIENT_SECRET=unused \
  caldav-share serve
```

## Setting up OIDC

Using Keycloak as an example:

1. In your realm, create a client (type: OpenID Connect, confidential access).
2. Set the valid redirect URI to `https://your-host/oidc/callback`.
3. Copy the client id and secret into `CALDAV_OIDC_CLIENT_ID` and
   `CALDAV_OIDC_CLIENT_SECRET`.
4. Set `CALDAV_OIDC_ISSUER` to the realm URL, for example
   `https://sso.example/realms/main`.
5. Put your own email in `CALDAV_OIDC_ADMIN_EMAILS` so your first login is an
   admin.

Other providers follow the same shape: register a confidential client, set the
redirect URL, and use the provider's discovery URL as the issuer.

## Email for invitations

Invitations to outside guests travel by email (iMIP). You need:

- An SMTP relay you already trust. Deliverability (SPF, DKIM, DMARC) is handled
  at the relay or in DNS for the From domain; calshare does not sign messages
  itself.
- An IMAP mailbox whose address you set as `CALDAV_IMIP_REPLY_ADDRESS`. Replies
  land there and calshare polls them to update who has accepted.

If you skip the IMAP mailbox, invitations still go out, but you will not see
replies. If you skip SMTP entirely, invitations to local members still work;
external guests are recorded but not emailed.

## First run

1. Start the container. It migrates the database and begins serving.
2. Open `https://your-host` and sign in. Your first login creates your account,
   and (because your email is in `CALDAV_OIDC_ADMIN_EMAILS`) makes you an admin.
3. Go to Calendars and create your first local calendar.
4. Go to Devices and generate an app password. On your Mac, iPhone,
   Thunderbird, or DAVx5, add a CalDAV account pointing at your host, using your
   email as the username and the generated password.

## Adding a subscription

Open Subscriptions, paste an ICS link, give it a name, and optionally a color
and a check interval. calshare fetches it right away and then on the interval.
Use "Check now" to force a refresh.

## Creating a share view

1. Open Views and create one. Choose a privacy preset:
   - Busy only: titles are replaced with a label, details hidden.
   - Titles: titles shown, details hidden.
   - Full: everything shown.
2. On the view's page, tick the calendars to include.
3. Adjust the include options (private, cancelled, tentative, free events).
4. Under "Share links", create a link for each recipient. You can set a
   password and an expiry. Copy the subscribe link and send it. To stop access,
   revoke the link.

## Inviting people

Add attendees to an event in your calendar client. Members on this server are
delivered the invitation directly; outside email addresses receive an email. As
people respond, their status updates on the event.

## Backup and restore

The database is a single SQLite file. Back it up live with:

```
sqlite3 /var/lib/caldav-share/db.sqlite ".backup '/path/to/backup.db'"
```

To restore: stop the service, replace the file, start the service.

## Troubleshooting

- Run `caldav-share doctor` to check configuration, the database, and time zone
  data.
- Sign-in fails: confirm the redirect URL registered with your provider exactly
  matches `${CALDAV_EXTERNAL_URL}/oidc/callback`, and that the issuer URL is
  reachable from the container.
- A calendar client will not connect: check that you used an app password (not
  your SSO password) and your email as the username.
- A subscription shows an error: open Subscriptions to see the last status and
  message, and use "Check now" to retry.
- External invitations are not arriving: confirm SMTP settings, then run
  `caldav-share imip drain` to send the queue and surface any error.

## License

calshare is released under CC BY-NC-SA 4.0. You may use, modify, and share it
for non-commercial purposes, and any changes you distribute must carry the same
license. See `LICENSE` for the full text.

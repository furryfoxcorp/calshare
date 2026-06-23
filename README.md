# calshare

A self-hosted CalDAV server for a small group. It stores events and tasks,
speaks read/write CalDAV to Apple Calendar, Thunderbird, and DAVx5, schedules
between local users and external guests over email, subscribes to outside ICS
feeds, and publishes private "share view" links so friends can follow a
filtered slice of your calendar.

This is a work in progress. The sections below describe what runs today; the
rest of the surface lands as the build continues.

## Status

Built so far:

- Configuration loading (defaults, TOML file, environment, flags)
- SQLite storage with migrations, users, app passwords, calendars, and objects
- iCalendar core: parsing, recurrence expansion, emit, and VTIMEZONE bundling

## Build

You need Go 1.26 or newer.

```
go build ./cmd/caldav-share
```

The binary is fully static (the SQLite driver is pure Go), so it runs on a
distroless or scratch base image with no host dependencies.

## Release packaging

Cross-platform release archives are built with
[giftwrap](https://github.com/indrora/giftwrap). The config lives at
`.github/giftwrap.yml`. Tag a commit with a semver tag and run:

```
giftwrap release
```

## License

calshare is released under CC BY-NC-SA 4.0. You may use, modify, and share it
for non-commercial purposes, and any changes you distribute must carry the
same license. See `LICENSE` for the full text.

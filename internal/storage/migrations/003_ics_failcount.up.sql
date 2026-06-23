-- Track consecutive ICS poll failures so the poller can back off.
ALTER TABLE calendars ADD COLUMN ics_fail_count INTEGER NOT NULL DEFAULT 0;

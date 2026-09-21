-- What kind of device this is (server, switch, router, ...). Existing rows
-- default to 'Server' since the inventory was compute-only until now.
ALTER TABLE devices ADD COLUMN type TEXT NOT NULL DEFAULT 'Server';

-- Comma-separated subset of the allowed drive types, e.g. 'SSD,HDD'.
ALTER TABLE devices ADD COLUMN drive_types TEXT NOT NULL DEFAULT '';

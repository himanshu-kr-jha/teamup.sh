-- The address around a field work check-in, told by the geocoder when it was
-- taken (the owner's request of 16 November 2026). Empty when it could not be.
ALTER TABLE field_checkins ADD COLUMN address TEXT NOT NULL DEFAULT '';

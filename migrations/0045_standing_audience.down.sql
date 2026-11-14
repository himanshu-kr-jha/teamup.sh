DROP FUNCTION IF EXISTS standing_audience_includes(UUID, UUID);
DROP TABLE IF EXISTS standing_meeting_people;
DROP TABLE IF EXISTS standing_meeting_teams;
ALTER TABLE standing_meetings DROP COLUMN IF EXISTS audience;

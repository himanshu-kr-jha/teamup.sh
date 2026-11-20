ALTER TABLE scrum_entries DROP COLUMN IF EXISTS position;
ALTER TABLE scrum_participants DROP COLUMN IF EXISTS leads;
ALTER TABLE scrum_participants DROP COLUMN IF EXISTS position;
DROP TABLE IF EXISTS scrum_task_orders;
DROP TABLE IF EXISTS scrum_people_orders;

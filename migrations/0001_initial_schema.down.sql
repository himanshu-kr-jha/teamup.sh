-- Migration: initial_schema
-- Rollback: drop every table this migration created.
--
-- Explicit order, not CASCADE. CASCADE would make this statement succeed
-- against a schema that is not the one 0001 built: if a later migration's own
-- down had failed or been skipped, CASCADE would quietly take its tables with
-- it and report success, and the operator would learn what was actually
-- dropped from the empty database rather than from an error. Dropping in the
-- exact reverse of creation order fails loudly instead, naming the dependency
-- that does not belong here, which is the more useful outcome for a rollback.
--
-- The order below is the reverse of the CREATE TABLE order in the up
-- migration. That is sufficient by construction: every foreign key in 0001
-- points at a table created earlier (self-references aside, which a single
-- DROP handles), so reversing creation order always drops dependants first.
--
-- It was previously *almost* that order, with two tables out of place, and
-- both were fatal:
--
--   * attachments was dropped sixth, while task_attachments.attachment_id
--     still referenced it — the failure in the bug report.
--   * tasks was dropped before sprint_tasks.task_id, which would have failed
--     the same way at the next statement.
--
-- Since golang-migrate runs each migration in one transaction, that failure
-- rolled the whole statement back and left the database recorded at version -1
-- and dirty, which no later `migrate up` would touch.

DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS linked_identities;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS org_configs;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS push_subscriptions;
DROP TABLE IF EXISTS user_presence;
DROP TABLE IF EXISTS call_participants;
DROP TABLE IF EXISTS calls;
DROP TABLE IF EXISTS sprint_tasks;
DROP TABLE IF EXISTS task_activities;
DROP TABLE IF EXISTS task_watchers;
DROP TABLE IF EXISTS task_attachments;
DROP TABLE IF EXISTS task_comments;
DROP TABLE IF EXISTS task_labels;
DROP TABLE IF EXISTS labels;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS attachments;
DROP TABLE IF EXISTS sprints;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS message_reactions;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS channel_members;
DROP TABLE IF EXISTS channels;
DROP TABLE IF EXISTS team_members;
DROP TABLE IF EXISTS teams;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;

-- The extensions go last, once nothing that could depend on them is left.
DROP EXTENSION IF EXISTS "btree_gin";
DROP EXTENSION IF EXISTS "pg_trgm";
DROP EXTENSION IF EXISTS "pgcrypto";
DROP EXTENSION IF EXISTS "uuid-ossp";

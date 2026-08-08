-- Migration: initial_schema
-- Rollback: drop all tables

DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS linked_identities;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS org_configs;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS attachments;
DROP TABLE IF EXISTS push_subscriptions;
DROP TABLE IF EXISTS user_presence;
DROP TABLE IF EXISTS call_participants;
DROP TABLE IF EXISTS calls;
DROP TABLE IF EXISTS task_activities;
DROP TABLE IF EXISTS task_watchers;
DROP TABLE IF EXISTS task_attachments;
DROP TABLE IF EXISTS task_comments;
DROP TABLE IF EXISTS task_labels;
DROP TABLE IF EXISTS labels;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS sprint_tasks;
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

DROP EXTENSION IF EXISTS "btree_gin";
DROP EXTENSION IF EXISTS "pg_trgm";
DROP EXTENSION IF EXISTS "pgcrypto";
DROP EXTENSION IF EXISTS "uuid-ossp";

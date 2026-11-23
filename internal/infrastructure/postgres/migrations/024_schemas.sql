-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- The tables move out of public into three schemas, one per domain (spec D11):
-- identity holds accounts, sign-in challenges and tokens; tenancy holds
-- organizations, memberships, invites and slugs; platform holds feature flags,
-- the outbox and the audit trail. Separate schemas keep the data of each domain
-- apart, so a later grant or backup can cover one of them. goose_db_version
-- stays in public, where goose and the legacy adoption look for it.
--
-- The schemas are created strictly (no IF NOT EXISTS): a database that already
-- has one of them is not in the state this migration expects, and the failure
-- rolls the whole file back.
CREATE SCHEMA identity;
CREATE SCHEMA tenancy;
CREATE SCHEMA platform;

-- SET SCHEMA is a catalog change. It moves each table with its indexes,
-- constraints and owned sequences (identity columns and serial defaults), and
-- foreign keys keep pointing at the same tables, so no row is copied and no
-- data is rewritten. It takes an ACCESS EXCLUSIVE lock on each table for the
-- moment of the change, which is why the timeouts above apply.
--
-- The app connects with search_path=identity,tenancy,platform,public (package
-- searchpath). Unqualified names therefore still resolve: the outbox relay,
-- which accepts only a plain table name, and older migrations keep working.
-- Application queries are schema-qualified anyway.
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE users SET SCHEMA identity;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE user_identities SET SCHEMA identity;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE otp_challenges SET SCHEMA identity;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE refresh_tokens SET SCHEMA identity;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE api_tokens SET SCHEMA identity;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE oauth_sign_in_states SET SCHEMA identity;

-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE organizations SET SCHEMA tenancy;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE organization_memberships SET SCHEMA tenancy;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE organization_invites SET SCHEMA tenancy;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE organization_slugs SET SCHEMA tenancy;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE organization_provisions SET SCHEMA tenancy;

-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE feature_flags SET SCHEMA platform;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE outbox_events SET SCHEMA platform;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- SET SCHEMA is a catalog change with no row rewrite; the tables are moved once, in this transaction, and the app only reaches them through the forced search_path and schema-qualified queries
ALTER TABLE audit_events SET SCHEMA platform;

-- +goose Down
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- Rollback moves every table back to public first. The schemas are dropped
-- only after they are empty: DROP SCHEMA without CASCADE (the default RESTRICT)
-- fails the rollback if a schema still holds an object, so no table or row is
-- lost by a rollback that missed one.
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE identity.users SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE identity.user_identities SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE identity.otp_challenges SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE identity.refresh_tokens SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE identity.api_tokens SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE identity.oauth_sign_in_states SET SCHEMA public;

-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE tenancy.organizations SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE tenancy.organization_memberships SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE tenancy.organization_invites SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE tenancy.organization_slugs SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE tenancy.organization_provisions SET SCHEMA public;

-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE platform.feature_flags SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE platform.outbox_events SET SCHEMA public;
-- squawk-ignore prefer-robust-stmts, ban-set-schema -- rollback of the move made by this migration; goose runs this file in one transaction, so a failed rollback leaves every table where it was
ALTER TABLE platform.audit_events SET SCHEMA public;

DROP SCHEMA identity;
DROP SCHEMA tenancy;
DROP SCHEMA platform;

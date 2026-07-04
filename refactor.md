

---

# Architecture & Refactoring Specification: Headless AI Billing Gateway

This document outlines the requirements and design specifications for refactoring `new-api` into a lightweight, stateless, **Headless AI Billing and Routing Gateway**.

All frontend UIs, administrator tools, cron jobs, database migrations, and native configuration CRUDs are removed. Configuration management and schema versioning are decoupled and handled by a separate, standalone `new-api` central master instance.

---

## 🏗️ 1. System Architecture Overview

The refactored gateway functions purely as a high-performance, stateless HTTP reverse proxy and atomic billing node. It shares a PostgreSQL database cluster with the Central Management Service to read channel routes and update token logs without managing database states or possessing administrator logic.

```
[ Wails / Client Frontends ]
            │
            ▼ (Requests signed with custom sk-... tokens)
[ Headless AI Billing Gateway (This Refactored App) ]
            │
            ├──► Read Channels & Models (Shared DB - Static View)
            ├──► Atomically Deduct Quota & Append Logs (Shared DB - Static View)
            │
            ▼ (Proxying standard OpenAI / Claude payloads)
[ Upstream AI Suppliers (OpenAI, Gemini, Local Ollama, Hermes Agent) ]

```

---

## 🪓 2. Deletion & Stripping Manifest

The following modules must be physically removed from the codebase to eliminate overhead and maintenance drag:

### Administrator Functionality

* **Remove Admin Subsystems**: Delete all logic identifying administrative roles (`role == 1` or `IsAdmin`).
* **Strip Global Dashboard Data**: Remove controllers providing cross-user analytical counters, system-wide log viewers, or global user lists (`/api/user` admin listing, `/api/log` global view).
* Remove any setup seed scripts or default admin-provisioning data blocks.

### Database Management & Migrations

* **Remove GORM/SQL Migrations**: Completely disable or remove the `model.InitDb()` or `db.AutoMigrate()` execution paths from the boot process. The gateway expects an already structured, active table layout.

### Frontend & Asset Routings

* Remove all React/Vue build pipelines, static asset embeds, and embedded SPAs.
* Delete `router.Use(static.Serve(...))` or equivalent frontend mounting middlewares.

### Authentication Stripping

* Remove native username/password login capabilities (`POST /api/user/login`).
* Remove persistent OAuth-based session login logic. Third-party authentication is locked purely into a pipeline for user validation and automated profile binding during registration.

### Native Configuration Admin APIs

* Remove all native management controllers and endpoints for manually configuring channels, model multipliers, and redemption codes (`/api/channel`, `/api/redemption`, etc.).

### Automation & Background Chores (Cron)

* Eliminate all tickers (`go loop()`), schedulers, and background sync routines.
* Remove channel latency auto-testing, automated balance refreshers, and log rotation workers. All system state transitions must be purely event-driven and synchronous.

---

## ⚡ 3. Retained Core Capabilities

The refactored gateway must retain and optimize the following core features:

### 1. Headless Registration & Anti-Bot Verification (Pure JSON)

Since there is no frontend, all onboarding, OAuth-driven registration, and verification flows accept and yield pure JSON payloads or clean system redirects.

* **Anti-Bot Verification (Turnstile / CAPTCHA)**:
* **Validation Driver**: Retain `common/verification.go` to securely handle server-to-server validation with Cloudflare Turnstile or Google reCAPTCHA endpoints using system secrets (`TURNSTILE_SECRET_KEY`).
* Every registration-related or code-generation payload must include a client-side generated challenge token (`turnstile_token`). The gateway performs a synchronous, blocking HTTP POST to verify the token before continuing.
* **Email Verification (Protected by Turnstile)**:
* **SMTP Driver**: Retain `common/email.go` and SMTP configuration parameters to send verification codes.
* `POST /api/verification/email`: Accepts a JSON payload containing `email` and `turnstile_token`. Verifies anti-bot status first, then generates a random 6-digit numeric verification code, caches it in memory (Redis/Go-Cache with TTL), and dispatches it via SMTP.
* **Native Registration (With Email Verification)**:
* `POST /api/user/register`: Accepts raw JSON credentials (`username`, `password`, `email`, `code`). Verifies the email token `code` from the cache, salts/hashes the password via bcrypt, provisions a new record in the shared `users` table, and immediately returns a success status.
* **OAuth for Registration & Verification Only**:
* **Auth Drivers**: Retain OAuth2 code validation logic for GitHub/Google providers.
* `GET /api/oauth/github/login`: Generates and yields the secure 302 state-signed redirection URL.
* `GET /api/oauth/github/callback`: Consumes the callback code from the provider, extracts user attributes, verifies identity, auto-provisions a new record in the shared `users` table if the user doesn't exist, and passes the profile back to the client app.

### 2. Stateless Token Generation & Lifecycle APIs

* **Bearer Key Issuance**: Retain the capability to programmatically generate and manage functional `sk-...` API keys via programmatic JSON endpoints.
* `POST /api/token`: Creates a custom scoped `sk-` token for a given authenticated entity/Workspace.
* `GET /api/token`: Lists existing operational keys for the requester.
* `DELETE /api/token/:id`: Revokes token access synchronously.
* **Token Auth Middleware**: Intercepts `Authorization: Bearer sk-...` headers to extract contexts, confirm validity against the shared database, and map requests safely to their respective billing identities.

### 3. Streaming Relay & Atomic Billing Engine

* **Protocol Transpilation**: Maintain incoming standard OpenAI-compatible requests and match downstream mappings for proprietary engines (e.g., Google Gemini Native, Anthropic).
* **Streaming Token Counter**: Keep the server-sent events (SSE) text tokenizer layer (`tiktoken-go`) intact to precisely measure input/output tokens in real-time.
* **Synchronous Atomic Billing**: Deduct balances (`quota`) and log entries (`logs`) within a single database transaction context immediately upon request termination.
* **In-Flight Failover Routing**: Retain the ability to poll channel weights and seamlessly switch keys mid-request if the current downstream endpoint yields an explicit rate limit ($429$) or server fault ($500$).

---

## 🗄️ 4. Minimum Retained Database Schema (PostgreSQL)

The micro-gateway depends exclusively on these 4 structural tables from the shared DB context (**Read/Write Ops only; Structure is assumed managed**):

```sql
-- Main user/workspace entry containing current balance limits and credentials
TABLE users (
    id SERIAL PRIMARY KEY, -- Or transformed to UUID v7
    username VARCHAR(100),
    password_hash VARCHAR(100),
    email VARCHAR(100),
    github_id VARCHAR(100), -- Retained for OAuth registration mapping
    quota BIGINT DEFAULT 0,
    status INT DEFAULT 1,
    ...
);

-- The actual cryptographic keys generated for application authentication
TABLE tokens (id, user_id, key, status, ...);

-- Configured routing keys and provider URLs handled by the Master Central application 
TABLE channels (id, type, base_url, secret, weight, ...);

-- The transactional logs of model expenditures used for audit trails
TABLE logs (id, user_id, token_name, quota_burned, tokens_consumed, ...);

```

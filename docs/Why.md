# Why DevPit Exists

## Executive Summary

Software engineers spend a significant portion of their day answering a
simple question:

"What should I work on next?"

Existing engineering tools provide repository views, project views, or
notification feeds. None provide a unified engineer-centric attention view.

DevPit exists to solve that problem.

## The Problem

Engineering work is fragmented across GitHub, GitLab, Forgejo, Gitea, Jira,
Slack, CI systems, deployment systems and more. Every platform provides
notifications and dashboards, but each assumes it is the center of the
workflow.

The engineer is the center of the workflow.

## Existing Solutions

Repository dashboards answer:

- What's happening in this repository?

Project management tools answer:

- What work is planned?

Analytics platforms answer:

- How is the team performing?

Notification systems answer:

- Something happened.

DevPit answers:

- What requires my attention right now?

## Repository-centric vs User-centric

Traditional:

Repository

- Pull Requests
- Issues
- CI
- Releases

DevPit:

Me

- Needs Review
- Changes Requested
- Blocked
- Ready to Merge
- Mentioned
- Waiting on Author
- Needs Backport (future)

Repositories become context rather than navigation.

## Attention Over Information

DevPit intentionally surfaces actionable work instead of displaying every
notification. The goal is to reduce cognitive load and context switching.

## Multi-provider by Design

Providers are peers rather than centers of the system.

Supported initially:

- GitHub
- GitLab

Future:

- Forgejo
- Gitea
- Jira
- Slack
- CI/CD
- Sentry
- PagerDuty

## The Attention Engine

Provider-specific events are normalized into common events such as:

- ReviewRequested
- Mentioned
- PipelineFailed
- ConflictDetected
- BackportNeeded

These are transformed into actionable states like Needs Review, Blocked,
Ready to Merge, and Waiting on Author.

## User-centric Synchronization

Rather than mirroring hundreds of repositories, DevPit discovers work from:

- Review requests
- Mentions
- Assigned work
- Authored merge requests

Repository details are fetched only when needed.

## Self-host First

DevPit runs as:

- A single executable
- A Docker container

No server-side plugins or modifications to GitHub, GitLab, or other
providers are required.

## Read-only by Default

DevPit aggregates information rather than replacing existing platforms.
Actions remain in the source systems unless optional integrations are added
later.

## Guiding Principles

- User-centric
- Attention over information
- Provider agnostic
- Self-host first
- Read-only by default
- Extensible plugin architecture
- Fast and keyboard friendly

## Success Criteria

Within 30 seconds of opening DevPit, an engineer should know:

1. What needs my attention?
2. What is blocking me?
3. What am I blocking?
4. Which reviews should I complete?
5. Which release or backport tasks require action?

## Positioning Statement

DevPit is an open-source attention center for software engineers. It
aggregates actionable work from code forges, issue trackers, CI/CD systems,
and collaboration tools into a single, user-centric dashboard.

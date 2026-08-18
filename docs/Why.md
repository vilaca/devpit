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

## The Attention Engine

Provider-specific events are normalized into common events — reviews requested,
mentions, CI failures, assignment changes — then folded into neutral signals
like Changes Requested, Review Requested, Blocked, and Ready to Merge
(`docs/Attention_Engine.md`).

## User-centric Synchronization

Rather than mirroring hundreds of repositories, DevPit discovers work from:

- Review requests
- Mentions
- Assigned work
- Authored merge requests

Repository details are fetched only when needed.

## Guiding Principles

See `docs/Engineering_Philosophy.md` for the guiding principles — attention over
information, provider-agnostic peers, self-host first, and read-only by default.

## Success Criteria

Within 30 seconds of opening DevPit, an engineer should know:

1. What needs my attention?
2. What is blocking me?
3. What am I blocking?
4. Which reviews should I complete?
5. Which release or backport tasks require action? *(planned, not yet built —
   `docs/Roadmap.md`.)*

## Positioning Statement

DevPit is an open-source attention center for software engineers. Today it
aggregates actionable work from code forges (GitHub, GitLab), with optional
Jira ticket-status enrichment, into a single user-centric list. The peer
provider model is built to reach issue trackers, CI/CD, and collaboration tools
over time (`docs/Roadmap.md`).

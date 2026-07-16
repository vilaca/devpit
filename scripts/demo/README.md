# DevPit demo world

A committed, reusable mock of both forges. `demo-forge` (`main.go`) impersonates
a GitHub **and** a GitLab host well enough for the real providers
(`provider/github`, `provider/gitlab`) to run a full reconcile + fast-poll +
GraphQL-join cycle against it, serving the hand-editable JSON under `fixtures/`.

This is the permanent source of the hero screenshot (`docs/assets/hero.png`) and
every UI demo — **never** a real instance. It ships in no binary or image; it
imports no internal package (`.go-arch-lint.yml` `demo` component enforces the
isolation) and speaks only HTTP.

## Run it

```sh
scripts/demo/run.sh
```

Builds the SPA + devpit and the mock forge, wipes the scratch DB, starts both,
pins the "Handle next" item, and prints the URLs. Open <http://localhost:7474>;
Ctrl-C stops both. The forge listens on `localhost:9099`; `config.yaml` points
the two connections at `/gh` and `/gl` there.

## Extending the world = editing JSON

Every response is a file under `fixtures/`; there is no code to touch to change
what the demo shows. The provider derives its API sub-path from `base_url`, so
the demo host answers these routes:

| Provider call | Fixture file |
|---|---|
| GitHub `/user` | `github/user.json` |
| GitHub search (per role) | `github/search_{review-requested,assignee,author,user}.json` |
| GitHub notifications (fast-poll) | `github/notifications.json` |
| GitHub PR detail | `github/pulls/{owner}~{repo}~{number}.json` |
| GitHub collaborators (sole-merge probe) | `github/collaborators/{owner}~{repo}.json` |
| GitLab `/user` | `gitlab/user.json` |
| GitLab merge_requests (per scope) | `gitlab/merge_requests_{assigned_to_me,created_by_me,reviewer}.json` |
| GitLab todos (fast-poll) | `gitlab/todos.json` |
| GitLab owned projects (sole-merge) | `gitlab/projects.json` |
| GitLab MR detail | `gitlab/projects/{id}_mr_{iid}.json` |

The **GraphQL join** is keyed, not static: `github/graphql.json` and
`gitlab/graphql.json` are maps from an item key to that item's join facts —
`"owner/repo#number"` for GitHub, `"group/project!iid"` for GitLab. The forge
parses each batched query, looks up every aliased item, and rebuilds the
response. An item absent from the map returns a null node (the graceful-degrade
shape the join tolerates), so you only add a key when an item needs join-sourced
signals (missing approvals, checks running, auto-merge, rebase).

To add an item to the list: add it to the relevant `search_*`/`merge_requests_*`
scope file (that sets its role + age), and — if it needs a join-sourced signal —
add its key to the matching `graphql.json`. Payload shapes come straight from
the provider test fixtures (`testdata/fixtures/{github,gitlab}`); don't invent
fields. Age bands use timestamps **relative to now** in the fixtures so the
fresh/stale/old split doesn't rot — keep them relative when editing.

## Re-capture the hero screenshot

Re-shoot on any UI-visible release. Run `run.sh`, wait for both health dots to
go green and the list to fill (~60s — GitHub notification signals land on the
first fast-poll). The README renders the dark shot (`docs/assets/hero-dark.png`);
keep the light one (`docs/assets/hero.png`) current too. Headless Chrome does both
(light is the default; `--force-dark-mode` makes the app pick its own dark theme):

```sh
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
"$CHROME" --headless=new --hide-scrollbars --window-size=1120,1160 \
  --screenshot=docs/assets/hero.png http://localhost:7474
"$CHROME" --headless=new --hide-scrollbars --window-size=1120,1160 --force-dark-mode \
  --screenshot=docs/assets/hero-dark.png http://localhost:7474
```

A maintainer approves the shots before they land.

# Usage statistics

Denova release builds send optional product events directly to Umami Cloud's
`https://gateway.umami.is/api/send`, using Website ID
`23394fdd-4d21-4dd0-b833-16bfedff38d8`, `tag=app`, and the synthetic hostname
`app.denova-ai.com`. The public website uses the same Website ID and `tag=site`.
Always filter dashboards by tag; the Website overview and monthly allowance are
shared. No management API key is bundled with either application.

## User control and privacy

The first-run notice explains that collection is enabled by default. No event
is collected until the notice is dismissed. Users can turn it off immediately
or under Settings → General. Disabling cancels in-flight delivery, drops the
memory queue, and invalidates earlier execution handles. Re-enabling does not
upload earlier activity. The setting applies to the host user across Projects,
writing, games, and authenticated LAN clients.

A preference write failure also stops collection in memory and permits a save
retry; it never leaves the first-run notice blocking creation. Reading time
from before a disable/re-enable transition is discarded.

Source/development builds do not read host analytics state or send events.
Missing permissions, corrupt state, or another running process owning the host
analytics lease disable statistics without blocking creative data loading.

`os.UserConfigDir()/Denova/analytics.json` stores only the preference, notice
acknowledgement, random `app_<UUID>` identity, first active date, recent period
keys, today's bounded feature set, and three retention markers. This file is
outside portable `.denova` data and is not a Session recovery journal. No
operation history or offline event payloads are persisted. Reinstalling while
keeping this file preserves identity; deleting it starts a new observation.

Only explicitly constructed fields from `internal/analytics/events.go` cross
the network. They include fixed categories, version/platform, execution result,
duration bucket, dates, and a random identity. Manuscripts, prompts, model
replies, images, names, paths, business IDs, model names, API endpoints/keys,
raw errors, trace data, and journal content are excluded. Browser activity
requests accept only a feature enum and reject extra fields. Cloud receives the
connection IP; a persistent random ID makes this pseudonymous usage analysis,
not fully anonymous collection.

## Event catalog and metrics

| Event | Admission | Properties |
| --- | --- | --- |
| `app.first_active` | First observed user activity per identity | `cohort_date` |
| `app.active_day` | First user activity per UTC day | `version`, `platform` |
| `app.active_week` | First activity per ISO calendar week | None |
| `app.active_month` | First activity per calendar month | None |
| `app.retained_d1`, `app.retained_d7`, `app.retained_d30` | User activity exactly 1, 7, or 30 UTC days after first activity | `cohort_date` |
| `app.feature_used` | First use of a category per UTC day | `feature` |
| `app.run_finished` | One admitted Writing/Game/Chat execution or direct image request | `feature`, `runtime`, `trigger`, `outcome`, `duration_bucket` |
| `app.action_completed` | Successful project creation, content export, or version restore | `action` |

DAU/WAU/MAU are the event counts of their respective period events. WAU is an
ISO week and MAU a calendar month; these are not rolling 7/30-day uniques. A
backend process, reload, empty Writing/Game page, automatic saves without
changes, tool call, or sub-Agent does not independently count as activity.
Focused visible content reading counts after 30 seconds. Changed editor saves,
user execution admission, and completed explicit actions also count.
Automation-origin Chat executions carry `trigger=automation` and do not create
user activity, retention or daily feature events.

Feature reach is the count of `app.feature_used` for a feature, divided by DAU
over the same day. Execution results are bounded to success, failure, cancelled,
suspended and preempted; duration buckets are under 10 seconds, 10 seconds to
1 minute, 1–5 minutes and over 5 minutes. Internal runtime continuation/tool
rounds are not independently instrumented.

For each `cohort_date`, D7 retention is `app.retained_d7 / app.first_active`;
calculate only after the cohort has aged seven complete days. D1/D30 use the
same rule. These are first observed participating active instances, not new
downloads or natural persons. Several browsers/LAN devices share a host identity.
The website's download click does not prove download completion, and no identity
is transferred between website and App.

Umami's default visitor/retention implementation uses sessions, which can split
after network changes or salt rotation. Use the deduplicated events above for
product metrics instead of treating Website visitors as App users. Use event
property breakdowns or dashboard exports for cohort calculations.

## Delivery and verification

The queue holds at most 128 events in memory, with a five-second HTTP deadline.
Shutdown cancels delivery and waits at most one second. Delivery is best effort:
counter markers are saved before sending, and ambiguous/failed responses are
not retried to avoid duplicating count events. Offline activity is not backfilled.
Reported statistics therefore represent samples successfully received by Cloud.
Collection never delays an Agent's model execution on a network request.

The fixed `Mozilla/5.0 Denova/1.0` User-Agent was verified with Cloud. Bare
`Denova/1.0` is filtered as a bot despite HTTP 200. The exporter requires a
collection receipt with `cache` and `sessionId`; HTTP 200 alone is insufficient.
Real smoke checks use a synthetic identity and `tag=app-test`, excluded from
production dashboards. Automated tests use temporary host state and local HTTP
collectors; E2E source binaries stay off Cloud and the real user's state.

Umami's free plan currently retains six months and includes 100,000 monthly
units. Website pageviews, App custom events and custom event properties all
consume this shared allowance. Period and retention events are not sampled;
reduce high-frequency execution detail first if the allowance becomes tight.

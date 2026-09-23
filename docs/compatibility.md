# Cron compatibility

Expression validation, full-document preservation and runtime execution are separate concerns.

| Capability | System cron profile | Supercronic profile |
| --- | --- | --- |
| Fields | minute, hour, DOM, month, DOW | 5 fields; 6 adds year; 7 prepends seconds and adds year |
| Sunday | 0 or 7, SUN | Upstream cronexpr rules |
| Names/ranges/lists/steps | Supported, including named months/weekdays | Upstream ParseStrict |
| Macros | Common yearly/monthly/weekly/daily/hourly forms | Upstream supported macros |
| `@reboot` | Event trigger, no next timestamp | Rejected |
| `@every`, Quartz `?`, `L`, `W`, `#` | Rejected by this profile | Only syntax accepted by Supercronic's parser |
| Command `%` | Command/stdin separator unless escaped | Ordinary shell content |
| Environment assignments | Apply to following entries | Supercronic 0.2.49 uses document-wide final assignments |
| System username field | Read-only system source | Not supported |

The system adapter uses robfig/cron v3.0.1 with explicit Sunday-7 and Vixie leading-wildcard handling, excluding library-only extensions. Two restricted day fields use OR: `0 0 1 * 1` includes month beginnings and Mondays. Leading wildcard steps retain native day-field behavior.

Supercronic uses its in-tree cronexpr package at v0.2.49. Six-field `0 0 1 1 * 2028` constrains the year; seven-field `*/2 * * * * * *` schedules every two seconds. When a target Supercronic executable exists, file installation also invokes `-test` without running jobs.

lnquy/cron provides English/Traditional Chinese descriptions after validation. The adapter overrides misleading day-field AND prose. Descriptions do not determine validity or next runs.

Finite English grammar supports minute/hour intervals dividing their field exactly, daily times, weekdays and named weekdays. `every 7 minutes`, `every 90 minutes`, and `every other Friday` are rejected rather than approximated. Raw `*/7` remains valid cron: minute slots reset each hour. Builder choices share validation.

Timezone discovery reads the host's zone configuration; explicit host/source IANA overrides cover unknown discovery or Supercronic process-specific TZ. CRON_TZ support cannot be inferred from “Linux”; set `cron_tz=true` only for a known supporting system daemon. macOS system cron does not support that switch. Unknown remote timezone remains unknown.

Forecasts show schedule matches, not proof of execution. Daemons differ in DST catch-up behavior; sleep, downtime and queue delays are not simulated. The system parser searches five years for a match, so an empty result is not a proof a schedule can never match. Supercronic's year bounds also apply. Missing local times are not synthesized; repeated times display distinct UTC offsets.

Unsupported existing lines are preserved. This version does not manage systemd, launchd, Windows Task Scheduler, catch-up, retry or overlap policies.

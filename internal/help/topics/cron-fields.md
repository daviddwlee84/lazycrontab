# Reading cron fields

    */5       9-17       *          *          1-5
     |          |        |          |           |
   minute      hour   day/month    month      weekday

System cron has five fields. Each box holds a complete field, not one digit.
`*` means every value, `1,15` lists values, `9-17` is a range, and `*/5` is
every fifth value. `*5` is invalid: the slash before the step is required.
`*/` is an unfinished draft; complete it before using the expression.

Minutes run from 0 to 59, hours 0 to 23, days 1 to 31, months 1 to 12, and
weekdays 0 to 7 (0 and 7 are Sunday). Month and weekday names are supported.
Cron days refer to calendar days: a schedule on the 31st skips shorter months.
When both day-of-month and weekday are restricted, either matching day triggers.
Use the explanation and upcoming times to check your intent, including DST.

Supercronic accepts five fields, six with YEAR last, or seven with SECOND first
and YEAR last. Do not treat a six-field expression as seconds plus five fields.
The supported syntax depends on the selected dialect. Native `@reboot` means
daemon startup, not a predictable next time; it is not supported by Supercronic.

Playground is read-only. Use in new job copies the expression into a draft.
The target's runtime timezone controls execution; changing the display timezone
does not change any schedule.

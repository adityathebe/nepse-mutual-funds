The BS 2074–2083 month lengths in `internal/sources/nepalidate.go` and the
2082-01-01 BS = 2025-04-14 AD anchor come from
[Medic's bikram-sambat](https://github.com/medic/bikram-sambat/tree/2bb4363b807e049e517bc8d4f1a9f20e37e95d02):
`test-data/daysInMonth.json` and `test-data/toBik_euro.json`.
The calendar facts are reproduced as Go arrays; the conversion code is local.
Upstream is licensed under Apache-2.0; see `licenses/bikram-sambat.txt`.

Conversion intentionally supports only these verified years. Before BS 2084,
verify and extend the calendar against published Nepali calendars. Unknown years
fail the affected adapter rather than silently extrapolate dates.

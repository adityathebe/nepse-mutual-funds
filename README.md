A Go collector that publishes Nepal mutual-fund NAV histories as static JSON from official websites.
It runs daily through GitHub Actions and makes the data available as a static API through GitHub Pages.

Each fund in `data/manifest.json` has a `maturity_date` (`YYYY-MM-DD`) when one is known.
`sync` reads the dates from ShareSansar's mutual fund NAV table, including matured funds.
When that fetch fails or a fund is missing from it, the fund keeps the date already in the manifest.

# ⚠️ FULLY VIBE CODED

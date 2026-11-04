# deckstrat

deckstrat turns one card into a tested, rated, tournament-ready deck.

Today it can look up cards and prices, and check a TCG Live decklist against
Standard, Expanded or Gym Leader Challenge with a reason for every problem.

## Setup

Requires Go 1.26+.

Card data comes from [TCGdex](https://tcgdex.dev), which needs no key.
Prices come from [JustTCG](https://justtcg.com), which needs an API key.
Copy `.env.example.json` to `.env.json` and add your key:

```json
{
  "development": {
    "just_tcg_api_key": "tcg_..."
  }
}
```

Any setting can also come from an environment variable, which overrides the
file:

| Variable                     | Purpose                                        |
| ---------------------------- | ---------------------------------------------- |
| `DECKSTRAT_JUST_TCG_API_KEY` | JustTCG API key                                |
| `DECKSTRAT_CACHE_DIR`        | Catalog cache (default: user cache dir)        |
| `DECKSTRAT_ENV`              | Section of the config file (`development`)     |
| `DECKSTRAT_CONFIG`           | Config file path (default `.env.json`)         |

TCGdex responses are cached on disk for 24 hours.

## Commands

Search the card catalog:

```sh
go run ./cmd/search --name "Mega Lucario ex"
go run ./cmd/search --set MEG --number 77        # TCG Live set code and number
```

Search prices:

```sh
go run ./cmd/search --source justtcg --name "Mega Lucario ex"
```

Validate a decklist exported from TCG Live:

```sh
go run ./cmd/validate testdata/decks/lucario_standard.txt
go run ./cmd/validate --format glc --export deck.txt
pbpaste | go run ./cmd/validate -
```

`validate` exits 0 when the deck is legal, 1 when it is not and 2 on errors.

## Layout

| Path                      | What it holds                                         |
| ------------------------- | ----------------------------------------------------- |
| `internal/catalog`        | Card model and the `Catalog` interface                |
| `internal/catalog/tcgdex` | TCGdex catalog client                                 |
| `internal/price`          | Price quotes and the `price.Source` interface         |
| `internal/price/justtcg`  | JustTCG price client                                  |
| `internal/format`         | Format records (`formats/*.json`) and deck validation |
| `internal/deck`           | TCG Live decklist parsing, resolution and export      |
| `internal/config`         | Config loading                                        |
| `internal/httpcache`      | On-disk HTTP cache                                    |

Formats are data. A rotation or ban is an edit to
`internal/format/formats/*.json`, not a code change.

- Bans list specific printings by TCGdex ID, because different cards share
  names (only two of Lost Thunder's Unown are banned in Expanded).
  Reprints of a banned printing are banned with it. A ban with no
  printings covers every card with that name.
- Standard and Expanded bans come from pokemon.com's banned card list,
  GLC bans from gymleaderchallenge.com. `ban_list_checked` records when
  each list was last compared with its source.
- A set becomes legal 14 days after its release date
  (`set_legal_delay_days`). Earlier, cards from it are reported as
  `not-yet-legal`.

## Tests

```sh
go test ./...
```

Tests use recorded API responses and make no network calls.

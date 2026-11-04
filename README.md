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
`internal/format/formats/*.json`, not a code change. The ban lists are
not entered yet, and `validate` warns about this.

## Tests

```sh
go test ./...
```

Tests use recorded API responses and make no network calls.

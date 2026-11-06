# deckstrat

deckstrat turns one card into a tested, rated, tournament-ready deck.

Today it can look up cards and prices, check a TCG Live decklist against
Standard, Expanded or Gym Leader Challenge with a reason for every problem,
and build a first deck around a starter Pokémon.

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
| `JUSTTCG_API_KEY`            | JustTCG API key, if the above is unset         |
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

`--export` writes the deck back in TCG Live's format. Lines that came in
with a set code are kept as written; lines without one get a printing from
the catalog. Both kinds import into TCG Live and the Limitless deck
builder (checked 2026-11-05 with a 60-card Standard list).

### Building from a starter

```sh
go run ./cmd/strat "Dragapult ex TWM 130"
```

`strat` prints the starter's needs, every pick with the reason it is
there, Pokémon worth testing as partners, the format check and a TCG Live
export. It is v1 of the strat engine in PLAN.md:

- Picks come from card **tags**: needs and provides terms from a fixed
  vocabulary (`internal/tag/vocab.json`). A Stage 2 needs evolution
  acceleration, so Rare Candy goes in; Phantom Dive costs Fire and
  Psychic, so Fire and Psychic Energy and acceleration go in.
- Role counts and staples are hand-set in `data/strat/standard.json`
  until tournament co-occurrence data exists. Staples are checked against
  the legal pool, so a rotated card is skipped with a note.
- Only Pokémon starters are supported, and one engine Pokémon line is
  added from the config's list. Tech Pokémon are left to you; the
  partner list is where to start.

### Tags

```sh
go run ./cmd/tag pool                      # build today's legal pool (cached)
go run ./cmd/tag run                       # tag the pool with rules into data/tags.json
go run ./cmd/tag run --llm --limit 20      # also tag with Claude (one Message Batch)
go run ./cmd/tag show "Ultra Ball MEG 131" # a card's tags and why
go run ./cmd/tag eval                      # score against data/eval/tags.json
```

Tags come from two taggers. The rules tagger reads stats and text
patterns: free, and every tag quotes what matched. The Claude tagger sends
each card with a cached vocabulary prompt and a JSON schema, through the
Message Batches API at half price; it needs `ANTHROPIC_API_KEY` or an `ant
auth login` profile. Claude tags are kept on retagging, human tags win
over both, and entries marked `reviewed` are never changed. Tags attach to
the functional card, so reprints share them.

`data/eval/tags.json` is the hand-checked eval set. It has 15 seed cards;
the plan is about 200, and the rules were tuned on the seed cards, so add
unseen ones before trusting the scores.

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
| `internal/pool`           | A format's legal cards, one printing per card         |
| `internal/tag`            | Tag vocabulary, rules and Claude taggers, eval        |
| `internal/strat`          | The strat engine: starter to deck                     |

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

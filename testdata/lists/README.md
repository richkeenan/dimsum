# Subscription parser fixtures

These small fixtures are authored for dimsum and covered by the project's MIT
license. They use synthetic `.test` names, not excerpts from third-party feeds.
Filenames identify the catalog entry whose declared syntax the test exercises;
they do not represent that publisher's contents or filtering effectiveness.

- `stevenblack-unified.txt` exercises hosts entries, aliases, loopback boilerplate,
  and underscore labels.
- `hagezi-*.txt` and `oisd-*.txt` exercise supported domain-anchored adblock rules.
- `hagezi-tif-mini.txt` and `oisd-big.txt` include malformed domain entries. The
  parser must report and skip those entries while retaining valid rules.

`internal/lists/parse_test.go` covers unsupported modifiers, exceptions, malformed
input, resource limits, and whole-source rejection. The catalog intentionally
omits feeds whose required syntax dimsum does not support.

Run the parser and catalog checks from the repository root:

```sh
go test ./internal/lists ./internal/catalog
```

Actual subscription URLs and publisher attribution live in
`internal/catalog/catalog.go`. Downloaded lists retain their publishers' terms;
the dimsum license does not relicense those downloads. Each refresh parses new
contents before activating them. A syntax fixture cannot guarantee that a live
feed will remain compatible.

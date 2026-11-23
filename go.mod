module github.com/cxyfer/notesteme

go 1.25.0

// web/client 的 npm 套件可能附帶 .go 檔；不讓 ./... 掃到它們（ADR-0008）。
ignore ./web/client/node_modules

require (
	github.com/cucumber/gherkin/go/v42 v42.0.1
	github.com/cucumber/godog v0.16.0
	github.com/cucumber/messages/go/v34 v34.2.0
	github.com/danielgtaylor/huma/v2 v2.34.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/pressly/goose/v3 v3.26.0
	github.com/spf13/pflag v1.0.10
	golang.org/x/crypto v0.42.0
	golang.org/x/sys v0.41.0
	golang.org/x/term v0.35.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.39.1
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/sethvargo/go-retry v0.5.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/exp v0.0.0-20250620022241-b7579e27df2b // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	modernc.org/libc v1.66.10 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

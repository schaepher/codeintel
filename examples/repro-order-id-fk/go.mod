module github.com/schaepher/codeintel/examples/repro-clearing-order-id-fk

go 1.26

require (
	github.com/schaepher/codeintel v0.0.0
	xorm.io/xorm v0.0.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.57.0 // indirect
)

replace github.com/schaepher/codeintel => ../..

replace xorm.io/xorm => ./xorm

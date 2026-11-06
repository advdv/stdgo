// Package stdtestpg holds what stdgo's own tests share about their PostgreSQL server.
package stdtestpg

// Image is the server image stdgo's tests run against, through stdpgxtestfx or
// stdtestcontainer.Postgres. Tests that use the same image share one container per `go test` run.
const Image = "postgres:17.2-alpine"

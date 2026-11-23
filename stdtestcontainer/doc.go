// Package stdtestcontainer provides re-usable code for using testcontainers-go.
//
// # Shared servers
//
// Some servers, such as [Postgres] and [S3], are started once per `go test`
// run and shared by every test, and every test package, of it, because starting
// one per test would make the tests too slow. They return a [Server] with the
// addresses to reach them at, and join the [Network], so containers that tests
// start reach them too.
//
// Share a server only when tests can isolate their state on it in another
// way, so parallel tests and test packages never see each other's data: each
// test gets its own database cloned from a migrated template (stdpgxtestfx),
// or its own randomly named buckets (stds3testfx). A server whose state tests
// cannot partition like that, such as one whose global configuration tests
// change, must not be shared: start one per test instead, and stop it when the
// test ends.
package stdtestcontainer

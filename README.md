# fastmail

[![Go Reference](https://pkg.go.dev/badge/github.com/ionut-maxim/fastmail.svg)](https://pkg.go.dev/github.com/ionut-maxim/fastmail)

A Go client for [Fastmail's JMAP API](https://www.fastmail.com/dev/), starting
with [Masked Email](https://www.fastmail.com/for-developers/masked-email/).

- `fastmail`: the top-level `Client`, one field per supported capability.
- `jmap`: a small generic JMAP (RFC 8620) client: session discovery, method
  calls, result references and typed errors.
- `maskedemail`: list, get, create, update and delete masked email addresses.

No dependencies outside the standard library.

## Install

```sh
go get github.com/ionut-maxim/fastmail
```

## Authentication

Create an API token in Fastmail under **Settings → Privacy & Security →
Manage API tokens**, and give it the **Masked Email** scope. The client sends it as
`Authorization: Bearer <token>`. Keep the token out of source code; read it
from the environment or a secret store.

## Example

```go
ctx := context.Background()
client, err := fastmail.New(os.Getenv("FASTMAIL_TOKEN"))
if err != nil {
	log.Fatal(err)
}

// List enabled addresses for example.com (and its subdomains).
list, err := client.MaskedEmail.List(ctx, &maskedemail.ListOptions{
	States:    []maskedemail.State{maskedemail.StateEnabled},
	ForDomain: "example.com",
})
if err != nil {
	log.Fatal(err)
}
for _, m := range list {
	fmt.Println(m.Email, m.Description)
}

// Create an address. New addresses default to "pending", which Fastmail
// deletes after 24 hours unless they receive mail, so ask for "enabled".
m, err := client.MaskedEmail.Create(ctx, maskedemail.CreateParams{
	State:       maskedemail.StateEnabled,
	ForDomain:   "https://example.com",
	Description: "Newsletter",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println("created", m.Email)
```

### Retiring an address

Disable an address to stop it (mail goes to the trash), or set it to
`deleted` (mail bounces):

```go
disabled := maskedemail.StateDisabled
_, err = client.MaskedEmail.Update(ctx, m.ID, maskedemail.UpdateParams{State: &disabled})
```

`MaskedEmail.Delete` permanently removes an address, but Fastmail only allows
that for addresses that never received mail.

### Errors

Errors are typed and match sentinels with `errors.Is`:

```go
switch {
case errors.Is(err, jmap.ErrRateLimit): // too many requests or creates
case errors.Is(err, jmap.ErrUnauthorized): // bad or expired token
case errors.Is(err, jmap.ErrUnsupportedCapability): // token lacks the scope
case errors.Is(err, jmap.ErrNotFound): // no such address
}
```

Use `errors.As` with `*jmap.RequestError`, `*jmap.MethodError` or
`*jmap.SetError` for details.

## Development

Tools are managed with [mise](https://mise.jdx.dev):

```sh
mise run check   # gofmt check, go vet, go test
mise run smoke   # read-only test against the real API, needs FASTMAIL_TOKEN
```

`go test ./...` never touches the network; tests run against an in-process
fake JMAP server. The live smoke test only lists addresses.

## Contributing

Commits follow [Conventional Commits](https://www.conventionalcommits.org)
(`feat:`, `fix:`, `docs:`, `chore:`, `ci:`, `test:`, `refactor:`; `!` for a
breaking change). Releases and the changelog are generated from them by
[release-please](https://github.com/googleapis/release-please).

## License

[MIT](LICENSE)

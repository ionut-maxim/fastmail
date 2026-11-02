package fastmail_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/ionut-maxim/fastmail"
	"github.com/ionut-maxim/fastmail/jmap"
	"github.com/ionut-maxim/fastmail/maskedemail"
)

func Example() {
	ctx := context.Background()
	client, err := fastmail.New(os.Getenv("FASTMAIL_TOKEN"))
	if err != nil {
		log.Fatal(err)
	}

	// List the enabled addresses created for example.com.
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

	// Create a new address that stays (pending ones expire after 24h).
	m, err := client.MaskedEmail.Create(ctx, maskedemail.CreateParams{
		State:       maskedemail.StateEnabled,
		ForDomain:   "https://example.com",
		Description: "Newsletter",
	})
	if errors.Is(err, jmap.ErrRateLimit) {
		log.Fatal("creating too many addresses, try again later")
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("created", m.Email)

	// Stop using it: disabling sends its mail to the trash.
	disabled := maskedemail.StateDisabled
	if _, err := client.MaskedEmail.Update(ctx, m.ID, maskedemail.UpdateParams{State: &disabled}); err != nil {
		log.Fatal(err)
	}
}

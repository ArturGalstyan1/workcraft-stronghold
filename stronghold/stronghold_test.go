package stronghold_test

import (
	"testing"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/stronghold"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
)

func TestStronghold(t *testing.T) {

	db, cleanUp := utils.GetDB()
	defer cleanUp()

	es := events.NewEventSender()
	s := stronghold.NewStronghold("abcd", db, es)
	go s.Run()
	t.Log("Ran stronghold")
}

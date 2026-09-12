package client

import (
	"fmt"
	"io"
)

func HandleAudioCommand(command Command, state *State, output io.Writer) bool {
	if command.Name != "/mute" && command.Name != "/deafen" {
		return false
	}
	mode := "toggle"
	if len(command.Arguments) == 1 {
		mode = command.Arguments[0]
	}
	if len(command.Arguments) > 1 || (mode != "on" && mode != "off" && mode != "toggle") {
		fmt.Fprintf(output, "usage: %s [on|off|toggle]\n", command.Name)
		return true
	}
	muted, deafened, _ := state.Audio.Snapshot()
	value := muted
	if command.Name == "/deafen" {
		value = deafened
	}
	switch mode {
	case "on":
		value = true
	case "off":
		value = false
	case "toggle":
		value = !value
	}
	if command.Name == "/mute" {
		state.Audio.SetMuted(value)
	} else if err := state.Audio.SetDeafened(value); err != nil {
		fmt.Fprintf(output, "deafen failed: %v\n", err)
		return true
	}
	fmt.Fprintf(output, "%s: %t\n", command.Name[1:], value)
	return true
}

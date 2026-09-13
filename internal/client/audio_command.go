package client

import (
	"fmt"
	"io"
	"strconv"
)

func HandleAudioCommand(command Command, state *State, output io.Writer) bool {
	if command.Name == "/rnnoise-gate" {
		if len(command.Arguments) != 1 {
			fmt.Fprintln(output, "usage: /rnnoise-gate <0..1>")
			return true
		}
		value, err := strconv.ParseFloat(command.Arguments[0], 32)
		if err != nil || state.Audio.SetRNNoiseGate(float32(value)) != nil {
			fmt.Fprintln(output, "rnnoise-gate must be a number between 0 and 1")
			return true
		}
		fmt.Fprintf(output, "rnnoise-gate: %.2f\n", value)
		return true
	}
	if command.Name != "/mute" && command.Name != "/deafen" && command.Name != "/rnnoise" {
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
	} else if command.Name == "/rnnoise" {
		value, _ = state.Audio.RNNoiseSnapshot()
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
	} else if command.Name == "/rnnoise" {
		state.Audio.SetRNNoiseEnabled(value)
	} else if err := state.Audio.SetDeafened(value); err != nil {
		fmt.Fprintf(output, "deafen failed: %v\n", err)
		return true
	}
	fmt.Fprintf(output, "%s: %t\n", command.Name[1:], value)
	return true
}

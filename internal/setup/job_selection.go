package setup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/term"
)

func readYesNo(input io.Reader) (bool, bool, error) {
	var key [1]byte
	for {
		if _, err := io.ReadFull(input, key[:]); err != nil {
			return false, false, err
		}
		switch key[0] {
		case 'y', 'Y':
			return true, false, nil
		case 'n', 'N':
			return false, false, nil
		case 3:
			return false, true, nil
		}
	}
}

func initializeJobs(root string, input *os.File, output io.Writer, workflows []workflowChoice) error {
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return errors.New("could not enter interactive job selection mode")
	}
	// Restore before printing normal output or writing files.
	choices, apply, err := func() ([]jobChoice, bool, error) {
		defer term.Restore(int(input.Fd()), state)
		fmt.Fprint(output, "\r\nAdd quota conditions to individual jobs? [y/n] ")
		yes, cancelled, err := readYesNo(input)
		if err != nil {
			return nil, false, errors.New("could not read job selection answer")
		}
		if cancelled {
			fmt.Fprint(output, "\r\nJob selection cancelled; workflow selection has been preserved.\r\n")
			return nil, false, nil
		}
		if !yes {
			fmt.Fprint(output, "n\r\n")
			return nil, false, nil
		}
		fmt.Fprint(output, "y\r\n")
		choices, err := scanJobChoices(root, workflows)
		if err != nil {
			return nil, false, err
		}
		cursor := nextSelectableJob(choices, -1, 1)
		if cursor < 0 {
			fmt.Fprint(output, "No eligible jobs found.\r\n")
			for _, choice := range choices {
				fmt.Fprintf(output, "  %s / %s: %s\r\n", terminalText(filepath.Base(choice.path)), terminalText(choice.id), terminalText(choice.disabled))
			}
			return nil, false, nil
		}
		previousLines := 0
		for {
			width, height, err := term.GetSize(int(input.Fd()))
			if err != nil || height < 8 || width < 20 {
				width, height = 80, 24
			}
			lines := jobSelectionLines(choices, cursor, width, height)
			if previousLines > 0 {
				fmt.Fprintf(output, "\x1b[%dA", previousLines)
			}
			for _, line := range lines {
				fmt.Fprintf(output, "\r\x1b[2K%s\r\n", line)
			}
			for i := len(lines); i < previousLines; i++ {
				fmt.Fprint(output, "\r\x1b[2K\r\n")
			}
			if previousLines > len(lines) {
				fmt.Fprintf(output, "\x1b[%dA", previousLines-len(lines))
			}
			previousLines = len(lines)
			key, err := readChecklistKey(input)
			if err != nil {
				return nil, false, errors.New("could not read job selection")
			}
			switch key {
			case "up", "down":
				direction := 1
				if key == "up" {
					direction = -1
				}
				if next := nextSelectableJob(choices, cursor, direction); next >= 0 {
					cursor = next
				}
			case "toggle":
				choices[cursor].selected = !choices[cursor].selected
			case "apply":
				fmt.Fprint(output, "\r\n")
				return choices, true, nil
			case "cancel":
				fmt.Fprint(output, "\r\nJob selection cancelled; workflow selection has been preserved.\r\n")
				return nil, false, nil
			}
		}
	}()
	if err != nil {
		return err
	}
	if !apply {
		return nil
	}
	changed, err := applyJobChoices(root, choices)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Fprintln(output, "No job changes.")
	} else {
		fmt.Fprintln(output, "Updated job conditions:")
		for _, path := range changed {
			fmt.Fprintf(output, "  %s\n", path)
		}
	}
	configured := make(map[string]bool)
	for _, choice := range choices {
		if choice.selected && choice.disabled == "" {
			configured[choice.path] = true
		}
	}
	if len(configured) > 0 {
		fmt.Fprintln(output, "Each configured job defaults to a 50% quota threshold. Adjust the threshold in its workflow file.")
		fmt.Fprintln(output, "Existing custom thresholds have been preserved.")
		for _, workflow := range workflows {
			if configured[workflow.path] {
				fmt.Fprintf(output, "  %s\n", workflow.path)
			}
		}
	}
	return nil
}

func nextSelectableJob(choices []jobChoice, cursor, direction int) int {
	for i := cursor + direction; i >= 0 && i < len(choices); i += direction {
		if choices[i].disabled == "" {
			return i
		}
	}
	return -1
}

func terminalText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}

func fitTerminalLine(value string, width int) string {
	runes := []rune(terminalText(value))
	if len(runes) >= width {
		return string(runes[:width-2]) + "…"
	}
	return string(runes)
}

// The window is centered on the selected job. Repeating the first visible
// file heading keeps group context visible even when scrolling within a file.
func jobSelectionLines(choices []jobChoice, cursor, width, height int) []string {
	controls := []string{"Up/Down move, Space toggle, Enter apply, Ctrl-C cancel"}
	if width < 60 {
		controls = []string{"Up/Down move, Space toggle", "Enter apply, Ctrl-C cancel"}
	}
	if width < 28 {
		controls = []string{"↑/↓ Move · Space", "Enter Apply · ^C"}
	}
	capacity := height - 4 - len(controls) // title, spacer, footer, and spare row
	start := cursor
	rows := 2
	for start > 0 {
		cost := 1
		if choices[start-1].path != choices[start].path {
			cost++
		}
		if rows+cost > capacity/2 {
			break
		}
		rows += cost
		start--
	}
	lines := []string{"Select jobs for quota:", ""}
	path := ""
	used := 0
	last := start
	for i := start; i < len(choices); i++ {
		cost := 1
		if choices[i].path != path {
			cost++
		}
		if used+cost > capacity {
			break
		}
		choice := choices[i]
		if choice.path != path {
			lines = append(lines, "    "+filepath.Base(choice.path))
			path = choice.path
			used++
		}
		pointer, marker := "  ", " "
		if i == cursor {
			pointer = "> "
		}
		if choice.selected {
			marker = "x"
		}
		label := choice.id
		if choice.disabled != "" {
			marker = "-"
			label += " (manual editing: " + choice.disabled + ")"
		} else if choice.selected {
			label += " (" + choice.threshold + "%)"
		}
		lines = append(lines, pointer+"["+marker+"]   "+label)
		used++
		last = i
	}
	for used < capacity {
		lines = append(lines, "")
		used++
	}
	lines = append(lines, controls...)
	lines = append(lines, fmt.Sprintf("Jobs %d–%d of %d", start+1, last+1, len(choices)))
	for i := range lines {
		lines[i] = fitTerminalLine(lines[i], width)
	}
	return lines
}

package check

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/artineering/ros2pi/internal/errs"
)

// Render writes the human-readable report.
//
// The layout puts the verdict first and the value second, so a user scanning
// for trouble reads one column rather than parsing sentences. Detail and fixes
// are indented under the check they belong to, because an instruction detached
// from its cause is just more text.
func Render(w io.Writer, rep Report, colour bool) {
	c := palette(colour)

	for _, s := range rep.Sections {
		fmt.Fprintf(w, "\n%s\n", c.bold(s.Name))
		for _, e := range s.Results {
			fmt.Fprintf(w, "  %s  %-11s %s\n",
				c.status(e.Result.Status), e.Check.Title, e.Result.Value)

			for _, d := range e.Result.Detail {
				if d == "" {
					fmt.Fprintln(w)
					continue
				}
				fmt.Fprintf(w, "        %s\n", c.dim(d))
			}
			renderFix(w, c, e.Result.Fix)
		}
	}
	fmt.Fprintln(w)
	renderSummary(w, c, rep)
}

func renderFix(w io.Writer, c colours, f *errs.Fix) {
	if f == nil || len(f.Steps) == 0 {
		return
	}
	for _, s := range f.Steps {
		if s.Text != "" {
			fmt.Fprintf(w, "        %s %s\n", c.fixLabel("fix:"), s.Text)
		}
		if s.Cmd != "" {
			label := "     "
			if s.Text == "" {
				label = c.fixLabel("fix:")
			}
			fmt.Fprintf(w, "        %s %s\n", label, c.cmd(s.Cmd))
		}
	}
	if f.NeedsReboot {
		fmt.Fprintf(w, "        %s\n",
			c.dim("this changes a firmware setting read at boot, so it needs a reboot"))
	}
}

func renderSummary(w io.Writer, c colours, rep Report) {
	switch {
	case rep.Failed > 0:
		fmt.Fprintf(w, "%s\n", c.fail(fmt.Sprintf("%d problem(s) to fix.", rep.Failed)))
		if rep.Warned > 0 {
			fmt.Fprintf(w, "%d warning(s).\n", rep.Warned)
		}
		fmt.Fprintf(w, "\nEach one above has a fix under it. For the reasoning behind a check:\n")
		fmt.Fprintf(w, "  ros2pi check --explain <id>\n")
	case rep.Warned > 0:
		fmt.Fprintf(w, "%s\n", c.warn(fmt.Sprintf("No problems, %d warning(s).", rep.Warned)))
	default:
		fmt.Fprintf(w, "%s\n", c.ok("Everything checks out."))
	}
}

// RenderJSON writes the report as JSON, for scripts and bug reports.
func RenderJSON(w io.Writer, rep Report) error {
	type jsonResult struct {
		ID      string   `json:"id"`
		Section string   `json:"section"`
		Title   string   `json:"title"`
		Status  Status   `json:"status"`
		Value   string   `json:"value"`
		Detail  []string `json:"detail,omitempty"`
	}
	out := struct {
		Results []jsonResult `json:"results"`
		Failed  int          `json:"failed"`
		Warned  int          `json:"warned"`
	}{Failed: rep.Failed, Warned: rep.Warned}

	for _, s := range rep.Sections {
		for _, e := range s.Results {
			out.Results = append(out.Results, jsonResult{
				ID: e.Check.ID, Section: e.Check.Section, Title: e.Check.Title,
				Status: e.Result.Status, Value: e.Result.Value, Detail: e.Result.Detail,
			})
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// --- colour ---

type colours struct{ on bool }

func palette(on bool) colours { return colours{on: on} }

func (c colours) wrap(code, s string) string {
	if !c.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (c colours) bold(s string) string     { return c.wrap("1", s) }
func (c colours) dim(s string) string      { return c.wrap("2", s) }
func (c colours) ok(s string) string       { return c.wrap("32", s) }
func (c colours) warn(s string) string     { return c.wrap("33", s) }
func (c colours) fail(s string) string     { return c.wrap("31", s) }
func (c colours) cmd(s string) string      { return c.wrap("36", s) }
func (c colours) fixLabel(s string) string { return c.wrap("1;36", s) }

// status renders a fixed-width badge so the column stays aligned; alignment is
// what makes the report scannable.
func (c colours) status(s Status) string {
	switch s {
	case OK:
		return c.ok("ok  ")
	case Warn:
		return c.warn("warn")
	case Fail:
		return c.fail("FAIL")
	case Note:
		return c.dim("note")
	}
	return strings.ToLower(string(s))
}

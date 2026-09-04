package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/leonkozlowski/portle/internal/model"
	"github.com/spf13/cobra"
)

func TestListCommandHasLSAlias(t *testing.T) {
	root := newRootCommand(io.Discard, io.Discard)

	listCommand, _, err := root.Find([]string{"list"})
	if err != nil {
		t.Fatalf("find list command: %v", err)
	}
	lsCommand, _, err := root.Find([]string{"ls"})
	if err != nil {
		t.Fatalf("find ls alias: %v", err)
	}
	if listCommand != lsCommand {
		t.Fatalf("ls resolved to %q instead of %q", lsCommand.Name(), listCommand.Name())
	}
	if listCommand.Name() != "list" {
		t.Fatalf("command name = %q, want list", listCommand.Name())
	}
}

func TestUpAndDownCommands(t *testing.T) {
	root := newRootCommand(io.Discard, io.Discard)

	upCommand, _, err := root.Find([]string{"up"})
	if err != nil {
		t.Fatalf("find up command: %v", err)
	}
	downCommand, _, err := root.Find([]string{"down"})
	if err != nil {
		t.Fatalf("find down command: %v", err)
	}
	stopCommand, _, err := root.Find([]string{"stop"})
	if err != nil {
		t.Fatalf("find stop alias: %v", err)
	}
	if upCommand.Name() != "up" || downCommand.Name() != "down" {
		t.Fatalf("command names = %q/%q, want up/down", upCommand.Name(), downCommand.Name())
	}
	if downCommand != stopCommand {
		t.Fatalf("stop resolved to %q instead of down", stopCommand.Name())
	}
	if err := upCommand.Args(upCommand, nil); err != nil {
		t.Fatalf("up rejected an omitted name: %v", err)
	}
	if err := downCommand.Args(downCommand, nil); err != nil {
		t.Fatalf("down rejected an omitted name: %v", err)
	}
}

func TestEditAndDeleteCommands(t *testing.T) {
	root := newRootCommand(io.Discard, io.Discard)

	editCommand, _, err := root.Find([]string{"edit"})
	if err != nil {
		t.Fatalf("find edit command: %v", err)
	}
	deleteCommand, _, err := root.Find([]string{"delete"})
	if err != nil {
		t.Fatalf("find delete command: %v", err)
	}
	removeCommand, _, err := root.Find([]string{"remove"})
	if err != nil {
		t.Fatalf("find remove alias: %v", err)
	}
	if editCommand.Name() != "edit" || deleteCommand.Name() != "delete" {
		t.Fatalf("command names = %q/%q, want edit/delete", editCommand.Name(), deleteCommand.Name())
	}
	if deleteCommand != removeCommand {
		t.Fatalf("remove resolved to %q instead of delete", removeCommand.Name())
	}
	if err := editCommand.Args(editCommand, nil); err != nil {
		t.Fatalf("edit rejected an omitted name: %v", err)
	}
	if err := deleteCommand.Args(deleteCommand, nil); err != nil {
		t.Fatalf("delete rejected an omitted name: %v", err)
	}
	addCommand, _, err := root.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}
	if err := addCommand.Args(addCommand, nil); err != nil {
		t.Fatalf("add rejected an omitted pod: %v", err)
	}
}

func TestConfirmDelete(t *testing.T) {
	command := &cobra.Command{}
	command.SetIn(strings.NewReader("\x1b[B\r"))
	var output bytes.Buffer
	command.SetOut(&output)

	confirmed, err := confirmDelete(command, "web", true)
	if err != nil {
		t.Fatalf("confirm delete: %v", err)
	}
	if !confirmed {
		t.Fatal("selecting Yes did not confirm deletion")
	}
}

func TestDeleteConfirmationUsesPickerStyle(t *testing.T) {
	confirmation := &deleteConfirmation{question: "Delete web and bring it down?"}
	want := "> Delete web and bring it down?\n\n" +
		"> No\n" +
		"  Yes\n\n" +
		"  ↑/↓ choose · enter confirm · esc cancel\n"
	if got := confirmation.View().Content; got != want {
		t.Fatalf("confirmation view mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestTargetWizardBuildsAndReviewsTarget(t *testing.T) {
	wizard := newTargetWizard("Add target", model.Target{}, false)

	enterText := func(value string) {
		t.Helper()
		wizard.input.SetValue(value)
		wizard.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	}
	enterText("web")
	enterText("svc/web")
	enterText("")
	enterText("8080")
	enterText("")

	wizard.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	wizard.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	enterText("")
	wizard.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	wizard.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if wizard.step != len(targetWizardFields) {
		t.Fatalf("wizard step = %d, want review step %d (error %q)", wizard.step, len(targetWizardFields), wizard.err)
	}
	wantTarget := model.Target{
		Name:       "web",
		Namespace:  "default",
		Resource:   "svc/web",
		RemotePort: 8080,
		Protocol:   model.ProtocolHTTP,
		Portless:   true,
	}
	if wizard.target != wantTarget {
		t.Fatalf("wizard target = %#v, want %#v", wizard.target, wantTarget)
	}

	want := "> Add target [review]\n\n" +
		"  Name    Resource    Local        Remote    Status    Protocol    Namespace    Context\n" +
		"  web     svc/web     automatic    8080      ↓ Down    http        default      current\n\n" +
		"  Portless    Yes\n\n" +
		"  enter save · shift+tab back · esc cancel\n"
	if got := wizard.View().Content; got != want {
		t.Fatalf("review view mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}

	wizard.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !wizard.done {
		t.Fatal("enter did not save the reviewed target")
	}
}

func TestTargetWizardKeepsInvalidFieldActive(t *testing.T) {
	wizard := newTargetWizard("Add target", model.Target{}, false)
	wizard.input.SetValue("")
	wizard.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if wizard.step != 0 {
		t.Fatalf("wizard advanced to step %d with an empty name", wizard.step)
	}
	if wizard.err != "Name is required." {
		t.Fatalf("wizard error = %q", wizard.err)
	}
}

func TestRunTargetWizard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	command := &cobra.Command{}
	command.SetContext(ctx)
	command.SetIn(strings.NewReader("web\rsvc/web\r\r8080\r\r\x1b[B\r\r\x1b[B\r\r"))
	var output bytes.Buffer
	command.SetOut(&output)

	target, saved, err := runTargetWizard(command, "Add target", model.Target{})
	if err != nil {
		t.Fatalf("run target wizard: %v", err)
	}
	if !saved {
		t.Fatal("wizard did not save")
	}
	if target.Name != "web" || target.Resource != "svc/web" || target.RemotePort != 8080 {
		t.Fatalf("wizard target = %#v", target)
	}
	if target.Protocol != model.ProtocolHTTP || !target.Portless {
		t.Fatalf("wizard choices were not applied: %#v", target)
	}
}

func TestEligibleTargets(t *testing.T) {
	statuses := targetStatuses()

	down := eligibleTargets(statuses, selectDownTarget)
	if len(down) != 1 || down[0].Target.Name != "db" {
		t.Fatalf("down targets = %#v, want db", down)
	}
	up := eligibleTargets(statuses, selectUpTarget)
	if len(up) != 1 || up[0].Target.Name != "web" {
		t.Fatalf("up targets = %#v, want web", up)
	}
	all := eligibleTargets(statuses, selectAnyTarget)
	if len(all) != 2 {
		t.Fatalf("all targets count = %d, want 2", len(all))
	}
}

func TestTargetPickerUsesListView(t *testing.T) {
	picker := &targetPicker{
		action:   "bring down",
		statuses: targetStatuses(),
	}

	want := "> Port forwards [select one to bring down]\n\n" +
		"  Name    Resource    Local    Remote    Status    Protocol    Namespace    Context\n" +
		"> web     svc/web     19400    80        ↑ Up      http        default      current\n" +
		"  db      svc/db      —        5432      ↓ Down    tcp         default      current\n"
	if got := picker.View().Content; got != want {
		t.Fatalf("picker view mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestTargetPickerSelectsWithArrowKeys(t *testing.T) {
	picker := &targetPicker{statuses: targetStatuses()}

	picker.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if picker.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", picker.cursor)
	}
	_, command := picker.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if picker.selected != "db" {
		t.Fatalf("selected target = %q, want db", picker.selected)
	}
	if command == nil {
		t.Fatal("enter did not quit the picker")
	}
}

func TestPromptForTargetSelectsSecondRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var output bytes.Buffer

	name, err := promptForTarget(
		ctx,
		strings.NewReader("\x1b[B\r"),
		&output,
		"bring up",
		targetStatuses(),
	)
	if err != nil {
		t.Fatalf("prompt for target: %v", err)
	}
	if name != "db" {
		t.Fatalf("selected target = %q, want db", name)
	}
}

func TestRenderTargetTable(t *testing.T) {
	statuses := targetStatuses()

	var output bytes.Buffer
	if err := renderTargetTable(&output, statuses); err != nil {
		t.Fatalf("render target table: %v", err)
	}

	want := "> Port forwards [1 active · 2 configured]\n\n" +
		"  Name    Resource    Local    Remote    Status    Protocol    Namespace    Context\n" +
		"  web     svc/web     19400    80        ↑ Up      http        default      current\n" +
		"  db      svc/db      —        5432      ↓ Down    tcp         default      current\n"
	if output.String() != want {
		t.Fatalf("output mismatch\nwant:\n%s\ngot:\n%s", want, output.String())
	}
}

func targetStatuses() []model.TargetStatus {
	return []model.TargetStatus{
		{
			Target: model.Target{
				Name:       "web",
				Resource:   "svc/web",
				RemotePort: 80,
				Protocol:   model.ProtocolHTTP,
				Namespace:  "default",
			},
			Forward: &model.Forward{LocalPort: 19400},
		},
		{
			Target: model.Target{
				Name:       "db",
				Resource:   "svc/db",
				RemotePort: 5432,
				Protocol:   model.ProtocolTCP,
				Namespace:  "default",
			},
		},
	}
}

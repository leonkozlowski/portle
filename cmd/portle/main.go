package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/x/term"
	"github.com/leonkozlowski/portle/internal/app"
	"github.com/leonkozlowski/portle/internal/config"
	"github.com/leonkozlowski/portle/internal/model"
	"github.com/spf13/cobra"
)

var version = "dev"

const tableColumnGap = "    "

func main() {
	root := newRootCommand(os.Stdout, os.Stderr)
	if err := fang.Execute(context.Background(), root, fang.WithVersion(version)); err != nil {
		os.Exit(1)
	}
}

func newRootCommand(output, errorsOutput io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "portle",
		Short:         "Repeatable kubectl port-forward management",
		Long:          "Portle creates, brings up, brings down, and inspects named kubectl port-forwards from one configuration file.",
		Args:          cobra.NoArgs,
		RunE:          runList,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(output)
	root.SetErr(errorsOutput)
	root.AddCommand(
		newAddCommand(),
		newEditCommand(),
		newDeleteCommand(),
		newUpCommand(),
		newDownCommand(),
		newListCommand(),
		newOpenCommand(),
		newDoctorCommand(),
		newInitCommand(),
	)
	return root
}

func newAddCommand() *cobra.Command {
	var options app.AddPodOptions
	command := &cobra.Command{
		Use:   "add [pod]",
		Short: "Add a target",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				options.Pod = args[0]
				target, err := app.AddPod(options)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(
					cmd.OutOrStdout(),
					"✓ %s added → %s (%s) :%d\n",
					target.Name,
					target.Resource,
					target.Namespace,
					target.RemotePort,
				)
				return err
			}

			initial := model.Target{
				Name:       options.Name,
				Namespace:  options.Namespace,
				RemotePort: options.RemotePort,
				LocalPort:  options.LocalPort,
				Protocol:   options.Protocol,
				Context:    options.Context,
				Portless:   options.Portless,
			}
			target, saved, err := runTargetWizard(cmd, "Add target", initial)
			if err != nil || !saved {
				return err
			}
			if err := app.AddTarget(target); err != nil {
				return err
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"✓ %s added → %s (%s) :%d\n",
				target.Name,
				target.Resource,
				target.Namespace,
				target.RemotePort,
			)
			return err
		},
	}
	command.Flags().StringVar(&options.Name, "name", "", "initial target name (defaults to the pod name when provided)")
	command.Flags().StringVarP(&options.Namespace, "namespace", "n", "default", "Kubernetes namespace")
	command.Flags().IntVarP(&options.RemotePort, "port", "p", 0, "remote port (inferred for a pod when omitted)")
	command.Flags().IntVar(&options.LocalPort, "local-port", 0, "exact local port")
	command.Flags().Var(newProtocolValue(&options.Protocol), "protocol", "target protocol: tcp or http")
	command.Flags().StringVar(&options.Context, "context", "", "kubectl context")
	command.Flags().BoolVar(&options.Portless, "portless", false, "register an HTTP target with Portless")
	return command
}

type protocolValue struct {
	value *model.Protocol
}

func newProtocolValue(value *model.Protocol) *protocolValue {
	*value = model.ProtocolTCP
	return &protocolValue{value: value}
}

func (value *protocolValue) Set(input string) error {
	protocol := model.Protocol(strings.ToLower(input))
	if protocol != model.ProtocolTCP && protocol != model.ProtocolHTTP {
		return errors.New("must be tcp or http")
	}
	*value.value = protocol
	return nil
}

func (value *protocolValue) String() string {
	if value == nil || value.value == nil || *value.value == "" {
		return string(model.ProtocolTCP)
	}
	return string(*value.value)
}

func (value *protocolValue) Type() string {
	return "protocol"
}

func newEditCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "edit [name]",
		Short: "Edit a configured target",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, selected, err := targetForCommand(cmd, args, selectAnyTarget, "edit")
			if err != nil || !selected {
				return err
			}
			target, found, err := app.Target(name)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("unknown target %q", name)
			}
			if _, running, err := app.Running(name); err != nil {
				return err
			} else if running {
				return fmt.Errorf("target %q is up; run `portle down %s` before editing it", name, name)
			}

			updated, saved, err := runTargetWizard(cmd, "Edit target", target)
			if err != nil || !saved {
				return err
			}
			if updated == target {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "> %s unchanged\n", name)
				return err
			}
			if err := app.UpdateTarget(target, updated); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "✓ %s updated\n", updated.Name)
			return err
		},
	}
}

func newDeleteCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use:     "delete [name]",
		Aliases: []string{"remove", "rm"},
		Short:   "Delete a configured target",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, selected, err := targetForCommand(cmd, args, selectAnyTarget, "delete")
			if err != nil || !selected {
				return err
			}
			if _, found, err := app.Target(name); err != nil {
				return err
			} else if !found {
				return fmt.Errorf("unknown target %q", name)
			}
			_, running, err := app.Running(name)
			if err != nil {
				return err
			}
			if !yes {
				confirmed, err := confirmDelete(cmd, name, running)
				if err != nil {
					return err
				}
				if !confirmed {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "> Delete cancelled.")
					return err
				}
			}
			if _, err := app.DeleteTarget(name); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "✓ %s deleted\n", name)
			return err
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return command
}

func confirmDelete(cmd *cobra.Command, name string, running bool) (bool, error) {
	input := cmd.InOrStdin()
	output := cmd.OutOrStdout()
	if !streamSupportsPrompt(input) || !streamSupportsPrompt(output) {
		return false, errors.New("confirmation requires an interactive terminal; pass --yes to delete")
	}
	detail := ""
	if running {
		detail = " and bring it down"
	}
	confirmation := &deleteConfirmation{
		question: fmt.Sprintf("Delete %s%s?", name, detail),
		useColor: writerSupportsColor(output),
	}
	result, err := tea.NewProgram(
		confirmation,
		tea.WithContext(cmd.Context()),
		tea.WithInput(input),
		tea.WithOutput(output),
	).Run()
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return false, nil
		}
		return false, err
	}
	finalConfirmation, ok := result.(*deleteConfirmation)
	return ok && finalConfirmation.confirmed, nil
}

type deleteConfirmation struct {
	question  string
	cursor    int
	decided   bool
	confirmed bool
	useColor  bool
}

func (*deleteConfirmation) Init() tea.Cmd {
	return nil
}

func (confirmation *deleteConfirmation) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return confirmation, nil
	}
	switch key.String() {
	case "up", "down", "j", "k", "left", "right":
		confirmation.cursor = 1 - confirmation.cursor
	case "y":
		confirmation.confirmed = true
		confirmation.decided = true
		return confirmation, tea.Quit
	case "enter":
		confirmation.confirmed = confirmation.cursor == 1
		confirmation.decided = true
		return confirmation, tea.Quit
	case "n", "esc", "ctrl+c", "q":
		confirmation.decided = true
		return confirmation, tea.Quit
	}
	return confirmation, nil
}

func (confirmation *deleteConfirmation) View() tea.View {
	if confirmation.decided {
		return tea.NewView("")
	}
	styles := newWizardStyles(confirmation.useColor)
	var output strings.Builder
	fmt.Fprintf(&output, "%s %s\n\n", styles.title.Render(">"), confirmation.question)
	for index, option := range []string{"No", "Yes"} {
		prefix := "  "
		if confirmation.cursor == index {
			prefix = styles.cursor.Render(">") + " "
		}
		fmt.Fprintf(&output, "%s%s\n", prefix, option)
	}
	fmt.Fprintf(&output, "\n  %s\n", styles.muted.Render("↑/↓ choose · enter confirm · esc cancel"))
	return tea.NewView(output.String())
}

type wizardFieldID int

const (
	wizardName wizardFieldID = iota
	wizardResource
	wizardNamespace
	wizardRemotePort
	wizardLocalPort
	wizardProtocol
	wizardContext
	wizardPortless
)

type wizardOption struct {
	label string
	value string
}

type wizardField struct {
	id          wizardFieldID
	label       string
	hint        string
	placeholder string
	options     []wizardOption
}

var targetWizardFields = []wizardField{
	{id: wizardName, label: "Name", hint: "Used by `portle up` and `portle down`.", placeholder: "web"},
	{id: wizardResource, label: "Resource", hint: "Kubernetes resource, for example svc/web or pod/web.", placeholder: "svc/web"},
	{id: wizardNamespace, label: "Namespace", hint: "Blank uses default.", placeholder: "default"},
	{id: wizardRemotePort, label: "Remote port", hint: "Port exposed by the resource (1–65535).", placeholder: "8080"},
	{id: wizardLocalPort, label: "Local port", hint: "Blank selects an available port automatically.", placeholder: "automatic"},
	{
		id:    wizardProtocol,
		label: "Protocol",
		hint:  "HTTP targets can be opened in a browser.",
		options: []wizardOption{
			{label: "tcp", value: "tcp"},
			{label: "http", value: "http"},
		},
	},
	{id: wizardContext, label: "Context", hint: "Blank uses the current kubectl context.", placeholder: "current"},
	{
		id:    wizardPortless,
		label: "Portless",
		hint:  "Adds a stable .localhost URL; requires HTTP.",
		options: []wizardOption{
			{label: "No", value: "false"},
			{label: "Yes", value: "true"},
		},
	},
}

type targetWizard struct {
	title     string
	target    model.Target
	step      int
	input     textinput.Model
	option    int
	err       string
	done      bool
	cancelled bool
	useColor  bool
}

func newTargetWizard(title string, target model.Target, useColor bool) *targetWizard {
	if strings.TrimSpace(target.Namespace) == "" {
		target.Namespace = "default"
	}
	if target.Protocol == "" {
		target.Protocol = model.ProtocolTCP
	}
	input := textinput.New()
	input.Prompt = "> "
	input.CharLimit = 128
	input.SetWidth(64)
	inputStyles := input.Styles()
	inputStyles.Cursor.Blink = true
	if useColor {
		accent := lipgloss.Color("99")
		muted := lipgloss.Color("245")
		inputStyles.Focused.Prompt = lipgloss.NewStyle().Bold(true).Foreground(accent)
		inputStyles.Focused.Placeholder = lipgloss.NewStyle().Foreground(muted)
		inputStyles.Cursor.Color = accent
	}
	input.SetStyles(inputStyles)
	wizard := &targetWizard{
		title:    title,
		target:   target,
		input:    input,
		useColor: useColor,
	}
	wizard.prepareField()
	return wizard
}

func runTargetWizard(cmd *cobra.Command, title string, target model.Target) (model.Target, bool, error) {
	input := cmd.InOrStdin()
	output := cmd.OutOrStdout()
	if !streamSupportsPrompt(input) || !streamSupportsPrompt(output) {
		return model.Target{}, false, errors.New("target wizard requires an interactive terminal")
	}
	wizard := newTargetWizard(title, target, writerSupportsColor(output))
	result, err := tea.NewProgram(
		wizard,
		tea.WithContext(cmd.Context()),
		tea.WithInput(input),
		tea.WithOutput(output),
	).Run()
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return model.Target{}, false, nil
		}
		return model.Target{}, false, err
	}
	finalWizard, ok := result.(*targetWizard)
	if !ok || finalWizard.cancelled || !finalWizard.done {
		return model.Target{}, false, nil
	}
	return finalWizard.target, true, nil
}

func (wizard *targetWizard) Init() tea.Cmd {
	return wizard.input.Focus()
}

func (wizard *targetWizard) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := message.(tea.KeyPressMsg)
	if isKey {
		switch key.String() {
		case "esc", "ctrl+c":
			wizard.cancelled = true
			return wizard, tea.Quit
		}
	}

	if wizard.step == len(targetWizardFields) {
		if !isKey {
			return wizard, nil
		}
		switch key.String() {
		case "enter":
			wizard.done = true
			return wizard, tea.Quit
		case "b", "backspace", "shift+tab", "left":
			wizard.step--
			wizard.prepareField()
			return wizard, wizard.focusField()
		}
		return wizard, nil
	}

	field := targetWizardFields[wizard.step]
	if len(field.options) > 0 {
		if !isKey {
			return wizard, nil
		}
		switch key.String() {
		case "up", "k":
			wizard.option = (wizard.option - 1 + len(field.options)) % len(field.options)
		case "down", "j":
			wizard.option = (wizard.option + 1) % len(field.options)
		case "enter":
			if wizard.applyField(field, field.options[wizard.option].value) {
				return wizard, wizard.advance()
			}
		case "shift+tab", "left", "backspace":
			return wizard, wizard.back()
		}
		return wizard, nil
	}

	if isKey {
		switch key.String() {
		case "enter":
			if wizard.applyField(field, wizard.input.Value()) {
				return wizard, wizard.advance()
			}
			return wizard, nil
		case "shift+tab":
			return wizard, wizard.back()
		}
	}
	var command tea.Cmd
	wizard.input, command = wizard.input.Update(message)
	return wizard, command
}

func (wizard *targetWizard) advance() tea.Cmd {
	wizard.step++
	if wizard.step == len(targetWizardFields) {
		wizard.input.Blur()
		wizard.target.ApplyDefaults()
		if err := wizard.target.Validate(); err != nil {
			wizard.err = err.Error()
			wizard.step--
			wizard.prepareField()
			return wizard.focusField()
		}
		return nil
	}
	wizard.prepareField()
	return wizard.focusField()
}

func (wizard *targetWizard) back() tea.Cmd {
	if wizard.step == 0 {
		return nil
	}
	wizard.step--
	wizard.prepareField()
	return wizard.focusField()
}

func (wizard *targetWizard) focusField() tea.Cmd {
	if wizard.step >= len(targetWizardFields) || len(targetWizardFields[wizard.step].options) > 0 {
		wizard.input.Blur()
		return nil
	}
	return wizard.input.Focus()
}

func (wizard *targetWizard) prepareField() {
	wizard.err = ""
	field := targetWizardFields[wizard.step]
	if len(field.options) > 0 {
		wizard.input.Blur()
		value := wizard.fieldValue(field.id)
		wizard.option = 0
		for index, option := range field.options {
			if option.value == value {
				wizard.option = index
				break
			}
		}
		return
	}
	wizard.input.Placeholder = field.placeholder
	wizard.input.SetValue(wizard.fieldValue(field.id))
	wizard.input.CursorEnd()
	wizard.input.Focus()
}

func (wizard *targetWizard) fieldValue(field wizardFieldID) string {
	switch field {
	case wizardName:
		return wizard.target.Name
	case wizardResource:
		return wizard.target.Resource
	case wizardNamespace:
		return wizard.target.Namespace
	case wizardRemotePort:
		if wizard.target.RemotePort > 0 {
			return strconv.Itoa(wizard.target.RemotePort)
		}
	case wizardLocalPort:
		if wizard.target.LocalPort > 0 {
			return strconv.Itoa(wizard.target.LocalPort)
		}
	case wizardProtocol:
		return string(wizard.target.Protocol)
	case wizardContext:
		return wizard.target.Context
	case wizardPortless:
		return strconv.FormatBool(wizard.target.Portless)
	}
	return ""
}

func (wizard *targetWizard) applyField(field wizardField, value string) bool {
	wizard.err = ""
	value = strings.TrimSpace(value)
	switch field.id {
	case wizardName:
		if value == "" {
			wizard.err = "Name is required."
			return false
		}
		wizard.target.Name = value
	case wizardResource:
		if value == "" {
			wizard.err = "Resource is required."
			return false
		}
		wizard.target.Resource = value
	case wizardNamespace:
		wizard.target.Namespace = value
		if value == "" {
			wizard.target.Namespace = "default"
		}
	case wizardRemotePort:
		port, err := requiredPort(value)
		if err != nil {
			wizard.err = "Remote port must be between 1 and 65535."
			return false
		}
		wizard.target.RemotePort = port
	case wizardLocalPort:
		if value == "" {
			wizard.target.LocalPort = 0
			break
		}
		port, err := requiredPort(value)
		if err != nil {
			wizard.err = "Local port must be blank or between 1 and 65535."
			return false
		}
		wizard.target.LocalPort = port
	case wizardProtocol:
		wizard.target.Protocol = model.Protocol(value)
	case wizardContext:
		wizard.target.Context = value
	case wizardPortless:
		wizard.target.Portless = value == "true"
		if wizard.target.Portless && wizard.target.Protocol != model.ProtocolHTTP {
			wizard.err = "Portless requires the HTTP protocol."
			return false
		}
	}
	return true
}

func requiredPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("invalid port")
	}
	return port, nil
}

func (wizard *targetWizard) View() tea.View {
	if wizard.done || wizard.cancelled {
		return tea.NewView("")
	}
	styles := newWizardStyles(wizard.useColor)
	var output strings.Builder
	if wizard.step == len(targetWizardFields) {
		fmt.Fprintf(&output, "%s %s %s\n\n", styles.title.Render(">"), wizard.title, styles.muted.Render("[review]"))
		renderTargetReview(&output, wizard.target, styles)
		fmt.Fprintf(&output, "\n  Portless    %s\n", yesNo(wizard.target.Portless))
		fmt.Fprintf(&output, "\n  %s\n", styles.muted.Render("enter save · shift+tab back · esc cancel"))
		return tea.NewView(output.String())
	}

	field := targetWizardFields[wizard.step]
	fmt.Fprintf(
		&output,
		"%s %s %s\n\n  %s\n",
		styles.title.Render(">"),
		wizard.title,
		styles.muted.Render(fmt.Sprintf("[%d/%d]", wizard.step+1, len(targetWizardFields))),
		field.label,
	)
	if len(field.options) == 0 {
		fmt.Fprintf(&output, "  %s\n", wizard.input.View())
	} else {
		for index, option := range field.options {
			prefix := "  "
			if wizard.option == index {
				prefix = styles.cursor.Render(">") + " "
			}
			fmt.Fprintf(&output, "%s%s\n", prefix, option.label)
		}
	}
	if wizard.err != "" {
		fmt.Fprintf(&output, "\n  %s\n", styles.error.Render(wizard.err))
	} else {
		fmt.Fprintf(&output, "\n  %s\n", styles.muted.Render(field.hint))
	}
	footer := "enter continue · shift+tab back · esc cancel"
	if len(field.options) > 0 {
		footer = "↑/↓ choose · enter continue · shift+tab back · esc cancel"
	}
	fmt.Fprintf(&output, "\n  %s\n", styles.muted.Render(footer))
	return tea.NewView(output.String())
}

type wizardStyles struct {
	title   lipgloss.Style
	muted   lipgloss.Style
	cursor  lipgloss.Style
	heading lipgloss.Style
	name    lipgloss.Style
	down    lipgloss.Style
	error   lipgloss.Style
}

func newWizardStyles(useColor bool) wizardStyles {
	styles := wizardStyles{
		title:   lipgloss.NewStyle(),
		muted:   lipgloss.NewStyle(),
		cursor:  lipgloss.NewStyle(),
		heading: lipgloss.NewStyle(),
		name:    lipgloss.NewStyle(),
		down:    lipgloss.NewStyle(),
		error:   lipgloss.NewStyle(),
	}
	if useColor {
		accent := lipgloss.Color("99")
		muted := lipgloss.Color("245")
		styles.title = styles.title.Bold(true).Foreground(accent)
		styles.muted = styles.muted.Foreground(muted)
		styles.cursor = styles.cursor.Bold(true).Foreground(accent)
		styles.heading = styles.heading.Bold(true)
		styles.name = styles.name.Bold(true)
		styles.down = styles.down.Foreground(muted)
		styles.error = styles.error.Foreground(lipgloss.Color("196"))
	}
	return styles
}

func renderTargetReview(output io.Writer, target model.Target, styles wizardStyles) {
	headings := []string{"Name", "Resource", "Local", "Remote", "Status", "Protocol", "Namespace", "Context"}
	localPort := "automatic"
	if target.LocalPort > 0 {
		localPort = strconv.Itoa(target.LocalPort)
	}
	contextName := target.Context
	if contextName == "" {
		contextName = "current"
	}
	rows := [][]string{{
		target.Name,
		target.Resource,
		localPort,
		strconv.Itoa(target.RemotePort),
		"↓ Down",
		string(target.Protocol),
		target.Namespace,
		contextName,
	}}
	widths := columnWidths(headings, rows)
	_ = renderAlignedRow(output, headings, widths, repeatStyle(len(headings), styles.heading))
	rowStyles := make([]lipgloss.Style, len(headings))
	rowStyles[0] = styles.name
	rowStyles[4] = styles.down
	rowStyles[7] = styles.muted
	_ = renderAlignedRow(output, rows[0], widths, rowStyles)
}

func yesNo(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}

func newUpCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "up [name]",
		Short: "Bring up a port-forward",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, selected, err := targetForCommand(cmd, args, selectDownTarget, "bring up")
			if err != nil || !selected {
				return err
			}
			forward, reused, err := app.Up(name)
			if err != nil {
				return err
			}
			verb := "started"
			if reused {
				verb = "already running"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "↑ %s %s → %s\n", forward.Name, verb, app.ConnectionString(forward))
			return err
		},
	}
}

func newDownCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "down [name]",
		Aliases: []string{"stop"},
		Short:   "Bring down a port-forward",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, selected, err := targetForCommand(cmd, args, selectUpTarget, "bring down")
			if err != nil || !selected {
				return err
			}
			found, err := app.Down(name)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("no active forward for %q", name)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "↓ %s stopped\n", name)
			return err
		},
	}
}

type targetSelection int

const (
	selectDownTarget targetSelection = iota
	selectUpTarget
	selectAnyTarget
)

func targetForCommand(cmd *cobra.Command, args []string, selection targetSelection, action string) (string, bool, error) {
	if len(args) == 1 {
		return args[0], true, nil
	}

	input := cmd.InOrStdin()
	output := cmd.OutOrStdout()
	if !streamSupportsPrompt(input) || !streamSupportsPrompt(output) {
		return "", false, fmt.Errorf("target name required when not running interactively; pass `portle %s <name>`", cmd.Name())
	}

	statuses, err := app.List()
	if err != nil {
		return "", false, err
	}
	candidates := eligibleTargets(statuses, selection)
	if len(candidates) == 0 {
		message := "No port forwards are configured."
		if len(statuses) > 0 && selection == selectDownTarget {
			message = "All configured port forwards are already up."
		}
		if len(statuses) > 0 && selection == selectUpTarget {
			message = "No port forwards are up."
		}
		_, err := fmt.Fprintf(output, "> %s\n", message)
		return "", false, err
	}

	name, err := promptForTarget(cmd.Context(), input, output, action, candidates)
	return name, err == nil, err
}

func eligibleTargets(statuses []model.TargetStatus, selection targetSelection) []model.TargetStatus {
	candidates := make([]model.TargetStatus, 0, len(statuses))
	for _, status := range statuses {
		eligible := selection == selectAnyTarget ||
			(selection == selectDownTarget && status.Forward == nil) ||
			(selection == selectUpTarget && status.Forward != nil)
		if eligible {
			candidates = append(candidates, status)
		}
	}
	return candidates
}

func promptForTarget(ctx context.Context, input io.Reader, output io.Writer, action string, candidates []model.TargetStatus) (string, error) {
	picker := &targetPicker{
		action:   action,
		statuses: candidates,
		useColor: writerSupportsColor(output),
	}
	result, err := tea.NewProgram(
		picker,
		tea.WithContext(ctx),
		tea.WithInput(input),
		tea.WithOutput(output),
	).Run()
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return "", errors.New("selection cancelled")
		}
		return "", err
	}
	finalPicker, ok := result.(*targetPicker)
	if !ok || finalPicker.cancelled {
		return "", errors.New("selection cancelled")
	}
	return finalPicker.selected, nil
}

type targetPicker struct {
	action    string
	statuses  []model.TargetStatus
	cursor    int
	selected  string
	cancelled bool
	useColor  bool
}

func (*targetPicker) Init() tea.Cmd {
	return nil
}

func (picker *targetPicker) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return picker, nil
	}
	switch key.String() {
	case "up", "k":
		picker.cursor = (picker.cursor - 1 + len(picker.statuses)) % len(picker.statuses)
	case "down", "j":
		picker.cursor = (picker.cursor + 1) % len(picker.statuses)
	case "enter":
		picker.selected = picker.statuses[picker.cursor].Target.Name
		return picker, tea.Quit
	case "esc", "ctrl+c", "q":
		picker.cancelled = true
		return picker, tea.Quit
	}
	return picker, nil
}

func (picker *targetPicker) View() tea.View {
	if picker.selected != "" || picker.cancelled {
		return tea.NewView("")
	}
	var output strings.Builder
	_ = renderTargetTableView(&output, picker.statuses, picker.useColor, picker.cursor, picker.action)
	return tea.NewView(output.String())
}

func streamSupportsPrompt(stream any) bool {
	file, isFile := stream.(*os.File)
	return !isFile || term.IsTerminal(file.Fd())
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured targets",
		Args:    cobra.NoArgs,
		RunE:    runList,
	}
}

func runList(cmd *cobra.Command, _ []string) error {
	statuses, err := app.List()
	if err != nil {
		return err
	}
	if len(statuses) == 0 {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No targets configured.")
		return err
	}
	return renderTargetTable(cmd.OutOrStdout(), statuses)
}

func renderTargetTable(output io.Writer, statuses []model.TargetStatus) error {
	headings, rows, active := targetTableRows(statuses)
	styles := newTargetTableStyles(writerSupportsColor(output))

	if _, err := fmt.Fprintf(
		output,
		"%s Port forwards %s\n\n",
		styles.title.Render(">"),
		styles.summary.Render(fmt.Sprintf("[%d active · %d configured]", active, len(statuses))),
	); err != nil {
		return err
	}

	widths := columnWidths(headings, rows)
	if err := renderAlignedRow(output, headings, widths, repeatStyle(len(headings), styles.heading)); err != nil {
		return err
	}
	for row, cells := range rows {
		if err := renderAlignedRow(output, cells, widths, targetRowStyles(statuses[row], styles)); err != nil {
			return err
		}
	}
	return nil
}

func renderTargetTableView(output io.Writer, statuses []model.TargetStatus, useColor bool, cursor int, action string) error {
	headings, rows, _ := targetTableRows(statuses)
	styles := newTargetTableStyles(useColor)
	if _, err := fmt.Fprintf(
		output,
		"%s Port forwards %s\n\n",
		styles.title.Render(">"),
		styles.summary.Render(fmt.Sprintf("[select one to %s]", action)),
	); err != nil {
		return err
	}

	widths := columnWidths(headings, rows)
	if err := renderAlignedRow(output, headings, widths, repeatStyle(len(headings), styles.heading)); err != nil {
		return err
	}
	for row, cells := range rows {
		prefix := "  "
		if row == cursor {
			prefix = styles.cursor.Render(">") + " "
		}
		if err := renderAlignedRowWithPrefix(output, prefix, cells, widths, targetRowStyles(statuses[row], styles)); err != nil {
			return err
		}
	}
	return nil
}

func targetTableRows(statuses []model.TargetStatus) ([]string, [][]string, int) {
	headings := []string{"Name", "Resource", "Local", "Remote", "Status", "Protocol", "Namespace", "Context"}
	rows := make([][]string, 0, len(statuses))
	active := 0
	for _, status := range statuses {
		mark := "↓ Down"
		local := "—"
		if status.Forward != nil {
			mark = "↑ Up"
			local = fmt.Sprint(status.Forward.LocalPort)
			active++
		}
		contextName := status.Target.Context
		if contextName == "" {
			contextName = "current"
		}
		rows = append(rows, []string{
			status.Target.Name,
			status.Target.Resource,
			local,
			fmt.Sprint(status.Target.RemotePort),
			mark,
			string(status.Target.Protocol),
			status.Target.Namespace,
			contextName,
		})
	}
	return headings, rows, active
}

type targetTableStyles struct {
	title   lipgloss.Style
	summary lipgloss.Style
	heading lipgloss.Style
	name    lipgloss.Style
	up      lipgloss.Style
	down    lipgloss.Style
	context lipgloss.Style
	cursor  lipgloss.Style
}

func newTargetTableStyles(useColor bool) targetTableStyles {
	styles := targetTableStyles{
		title:   lipgloss.NewStyle(),
		summary: lipgloss.NewStyle(),
		heading: lipgloss.NewStyle(),
		name:    lipgloss.NewStyle(),
		up:      lipgloss.NewStyle(),
		down:    lipgloss.NewStyle(),
		context: lipgloss.NewStyle(),
		cursor:  lipgloss.NewStyle(),
	}
	if useColor {
		accent := lipgloss.Color("99")
		muted := lipgloss.Color("245")
		styles.title = styles.title.Bold(true).Foreground(accent)
		styles.summary = styles.summary.Foreground(muted)
		styles.heading = styles.heading.Bold(true)
		styles.name = styles.name.Bold(true)
		styles.up = styles.up.Foreground(lipgloss.Color("42"))
		styles.down = styles.down.Foreground(muted)
		styles.context = styles.context.Foreground(muted)
		styles.cursor = styles.cursor.Bold(true).Foreground(accent)
	}
	return styles
}

func targetRowStyles(status model.TargetStatus, tableStyles targetTableStyles) []lipgloss.Style {
	styles := make([]lipgloss.Style, 8)
	styles[0] = tableStyles.name
	styles[4] = tableStyles.down
	if status.Forward != nil {
		styles[4] = tableStyles.up
	}
	styles[7] = tableStyles.context
	return styles
}

func columnWidths(headings []string, rows [][]string) []int {
	widths := make([]int, len(headings))
	for column, heading := range headings {
		widths[column] = lipgloss.Width(heading)
	}
	for _, row := range rows {
		for column, cell := range row {
			widths[column] = max(widths[column], lipgloss.Width(cell))
		}
	}
	return widths
}

func repeatStyle(count int, style lipgloss.Style) []lipgloss.Style {
	styles := make([]lipgloss.Style, count)
	for index := range styles {
		styles[index] = style
	}
	return styles
}

func renderAlignedRow(output io.Writer, cells []string, widths []int, styles []lipgloss.Style) error {
	return renderAlignedRowWithPrefix(output, "  ", cells, widths, styles)
}

func renderAlignedRowWithPrefix(output io.Writer, prefix string, cells []string, widths []int, styles []lipgloss.Style) error {
	if _, err := io.WriteString(output, prefix); err != nil {
		return err
	}
	for column, cell := range cells {
		if column > 0 {
			if _, err := io.WriteString(output, tableColumnGap); err != nil {
				return err
			}
		}
		padding := ""
		if column < len(cells)-1 {
			padding = strings.Repeat(" ", widths[column]-lipgloss.Width(cell))
		}
		if _, err := io.WriteString(output, styles[column].Render(cell)+padding); err != nil {
			return err
		}
	}
	_, err := io.WriteString(output, "\n")
	return err
}

func writerSupportsColor(writer io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

func newOpenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "open <name>",
		Short: "Open a running HTTP target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			forward, found, err := app.Running(args[0])
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("no active forward for %q", args[0])
			}
			if forward.Protocol != model.ProtocolHTTP {
				return fmt.Errorf("target %q uses TCP and cannot be opened in a browser", args[0])
			}
			url := app.ConnectionString(forward)
			if err := openBrowser(url); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Opening %s\n", url)
			return err
		},
	}
}

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check dependencies, configuration, and state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			failed := false
			for _, check := range app.Doctor() {
				mark := "ok"
				if !check.OK {
					mark = "!!"
					failed = true
				}
				detail := strings.TrimSpace(check.Detail)
				if detail == "" {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  %-2s  %s\n", mark, check.Name); err != nil {
						return err
					}
				} else {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  %-2s  %-10s %s\n", mark, check.Name, detail); err != nil {
						return err
					}
				}
			}
			if failed {
				return errors.New("one or more checks failed")
			}
			return nil
		},
	}
}

func newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create ~/.config/portle/config.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := config.Init()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", path)
			return err
		},
	}
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "linux":
		command = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("opening a browser is not supported on %s", runtime.GOOS)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return command.Process.Release()
}

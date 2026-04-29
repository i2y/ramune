// Survey TUI — exercises Form (text / select / multi / confirm),
// Paginator, key chord sequences, and a final Markdown summary card.
//
//   ../../ramune run survey.tsx

declare const Ramune: any;
declare namespace JSX {
  interface IntrinsicElements { [k: string]: any }
  type Element = string;
}

const Box = Ramune.tui.Box;
const Text = Ramune.tui.Text;
const Stack = Ramune.tui.Stack;
const Form = Ramune.tui.Form;
const Help = Ramune.tui.Help;
const Markdown = Ramune.tui.Markdown;

type Step = "form" | "review";
type Model = {
  step: Step;
  form: any; // Ramune.tui.form state
};

const init = (): Model => ({
  step: "form",
  form: Ramune.tui.form.init([
    {
      name: "name",
      type: "text",
      label: "Your name",
      placeholder: "type and tab to advance",
      required: true,
    },
    {
      name: "stack",
      type: "select",
      label: "Primary stack",
      options: ["Go", "TypeScript", "Rust", "Python", "Other"],
      value: "TypeScript",
    },
    {
      name: "interests",
      type: "multi",
      label: "Interested in",
      options: ["TUI", "AOT", "FFI", "Hybrid runtime", "Web stack"],
      value: ["TUI", "Hybrid runtime"],
    },
    {
      name: "comments",
      type: "textarea",
      label: "Anything else? (enter for newline, ctrl+s to send)",
      rows: 3,
    },
    {
      name: "consent",
      type: "confirm",
      label: "Share results back?",
      value: true,
    },
  ]),
});

// Sequence-aware keymap: 'g g' jumps to the first field, 'G' to the last.
const keys = Ramune.tui.keymap({
  edit:    { keys: ["e"], help: { key: "e", desc: "edit" } },
  topField: { keys: ["g g"], help: { key: "gg", desc: "first field" } },
  endField: { keys: ["G"], help: { key: "G", desc: "last field" } },
  quit:    { keys: ["q", "ctrl+c", "esc"], help: { key: "q", desc: "quit" } },
});

type Msg = { type: "key"; key: string; runes: string; alt: boolean };

function update(model: Model, msg: Msg): Model | { _state: Model; _cmd: string } {
  if (msg.type !== "key") return model;
  if (keys.matches(msg, "quit")) return Ramune.tui.Cmd.quit(model);

  if (model.step === "review") {
    if (msg.key === "e" || msg.key === "esc") {
      return {
        ...model,
        step: "form",
        form: { ...model.form, submitted: false, errors: {} },
      };
    }
    return model;
  }

  // Form step. Try sequence shortcuts first; otherwise delegate to form.
  const seq = keys.match(msg);
  if (seq === "topField") {
    return { ...model, form: { ...model.form, focused: 0 } };
  }
  if (seq === "endField") {
    return { ...model, form: { ...model.form, focused: model.form.fields.length - 1 } };
  }

  const next = Ramune.tui.form.handleKey(model.form, msg);
  if (!next) return model;
  if (next.submitted) {
    return { ...model, step: "review", form: next };
  }
  return { ...model, form: next };
}

function summary(values: Record<string, any>): string {
  const interests = (values.interests || []).join(", ") || "_(none)_";
  const comments = (values.comments || "").trim() || "_(no comments)_";
  return [
    `# Hi ${values.name || "friend"} 👋`,
    "",
    `You're a **${values.stack}** dev, with curiosity in: ${interests}.`,
    "",
    `> ${comments.replace(/\n/g, "\n> ")}`,
    "",
    `**Share results?** ${values.consent ? "yes ✓" : "no ✗"}`,
    "",
    "---",
    "",
    "Thanks for going through the form. Press `e` to edit, `q` to quit.",
  ].join("\n");
}

function view(model: Model): string {
  if (model.step === "review") {
    const values = Ramune.tui.form.values(model.form);
    return (
      <Box border="rounded" padding={[1, 2]} fg="15">
        <Stack gap={1}>
          <Text bold fg="12" padding={[0, 1]}>survey results</Text>
          <Markdown content={summary(values)} theme="dark" width={60} />
          <Help keys={[{ key: "e", desc: "edit" }, { key: "q", desc: "quit" }]} />
        </Stack>
      </Box>
    );
  }
  return (
    <Box border="rounded" padding={[1, 2]} fg="15">
      <Stack gap={1}>
        <Text bold fg="12" padding={[0, 1]}>quick survey</Text>
        <Form state={model.form} />
        <Help keys={keys.helpEntries(["topField", "endField", "quit"])} />
      </Stack>
    </Box>
  );
}

Ramune.tui.run({ init, update, view }).then((final: Model) => {
  if (final.step === "review") {
    const values = Ramune.tui.form.values(final.form);
    console.log(JSON.stringify(values, null, 2));
  }
});

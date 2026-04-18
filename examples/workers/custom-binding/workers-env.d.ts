// Augment the global Env interface declared in workers/workers.d.ts
// so TypeScript knows about the EMAIL binding this project adds.
// Merging works because Env is declared as an open interface.
interface EmailOpts {
  to: string;
  subject: string;
  body: string;
}

interface EmailBinding {
  send(opts: EmailOpts): Promise<void>;
}

interface Env {
  EMAIL: EmailBinding;
}

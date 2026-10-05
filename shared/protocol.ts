import { z } from "zod";

const text = z.string().trim().min(1);

export const Source = z.object({
  kind: z.enum(["human", "squad-goal", "project-rule"]),
  ref: text,
});

export const Evidence = z.discriminatedUnion("kind", [
  z.object({
    kind: z.literal("file"),
    path: text,
    lines: z.tuple([z.number().int().min(1), z.number().int().min(1)]).optional(),
    quote: z.string().optional(),
  }),
  z.object({ kind: z.literal("command"), command: text, exit: z.number().int(), output: z.string() }),
  z.object({ kind: z.literal("doc"), ref: text, quote: text }),
]);

const Step = z.object({ id: text, text });

export const LeadApproach = z.object({
  approach: text,
  steps: z.array(Step).min(1),
  current: z.array(Step),
  risks: z.array(z.string()),
});

/** Words a short name may not start with: they read as an id ("t2", "d1", "s3") or an HC ("lead"). */
const LOOKS_LIKE_ID = /^([a-z]\d+|lead|peer|supervisor)(-|$)/;

/**
 * A short name that goes into titles and branches: lowercase words joined by "-", like "format" or
 * "stock-immutable". Spaces become "-" and letters are lowered, so "Stock Immutable" is approved too.
 */
function shortName(max: number, example: string) {
  return z.preprocess(
    (v) => (typeof v === "string" ? v.trim().toLowerCase().replace(/\s+/g, "-") : v),
    z
      .string()
      .min(2)
      .max(max)
      .regex(/^[a-z0-9]+(-[a-z0-9]+)*$/, `tên ngắn: chữ thường, số, nối bằng dấu -, tối đa ${max} ký tự, ví dụ ${example}`)
      .refine((name) => !LOOKS_LIKE_ID.test(name), `tên ngắn không được mở đầu giống một id (t2, d1, s3) hay tên HC (lead, peer, supervisor), ví dụ ${example}`),
  );
}

export const Brief = z.object({
  /** Short name of the task: "format", "stock-immutable". The objective carries the description. */
  title: shortName(24, "format, stock-immutable"),
  objective: text,
  acceptance: z.array(text).min(1),
  required: z.array(z.object({ text, source: Source })),
  unknowns: z.array(z.string()),
  scope: z.array(text).min(1),
  reads: z.array(text).optional(),
  checks: z.array(text).min(1),
  leadApproach: LeadApproach,
  model: z.string().optional(),
});

export const Patch = z
  .object({
    leadApproach: LeadApproach.optional(),
    acceptance: z.array(text).min(1).optional(),
    scope: z.array(text).min(1).optional(),
  })
  .refine((p) => p.leadApproach || p.acceptance || p.scope, { message: "patch phải sửa ít nhất một trong leadApproach, acceptance, scope" });

const Target = z
  .object({
    kind: z.enum(["approach", "step", "current", "acceptance", "scope"]),
    ref: z.string().optional(),
  })
  .refine((t) => (t.kind === "step" || t.kind === "current" ? !!t.ref : true), {
    message: "target.ref là bắt buộc khi kind là step hoặc current",
  });

export const Challenge = z.object({
  target: Target,
  claim: text,
  evidence: z.array(Evidence).min(1),
  impact: text,
  proposal: text,
  affects: z.array(z.string()),
});

const Comparison = z.object({ point: text, mine: text, lead: text, whyLeadIsFine: text });

const CheckRun = z.object({ command: text, exit: z.number().int(), output: z.string() });

// Supervisor
export const OpenSquad = z.object({
  goal: text,
  acceptance: z.array(text).min(1),
  constraints: z
    .array(z.object({ text, source: z.object({ kind: z.enum(["human", "project-rule"]), ref: text }) }))
    .default([]),
  /** Short name of the squad's area: "pricing", "checkout-ui". */
  slug: shortName(20, "pricing, checkout-ui"),
  model: z.string().optional(),
});
export const CloseSquad = z.object({ squad: text });
export const AmendSquad = z
  .object({
    squad: text,
    note: text,
    constraints: z.array(z.object({ text, source: z.object({ kind: z.enum(["human", "project-rule"]), ref: text }) })).default([]),
    acceptance: z.array(text).default([]),
  })
  .refine((a) => a.constraints.length || a.acceptance.length, { message: "phải thêm ít nhất một ràng buộc hoặc một acceptance" });
export const Status = z.object({ squad: z.string().optional() });

// Shared
export const Message = z.object({ to: text, text });
export const Ask = z.object({ question: text, bestGuess: z.string().optional() });
export const Answer = z.object({
  ask: text,
  text,
  source: z.enum(["human", "squad-goal"]).optional(),
});

// Lead
export const Plan = z.object({ briefs: z.array(Brief).min(1) });
export const Respond = z.discriminatedUnion("action", [
  z.object({ action: z.literal("concede"), challenge: text, patch: Patch }),
  z.object({ action: z.literal("hold"), challenge: text, counterEvidence: z.array(Evidence).min(1), reason: text }),
  z.object({
    action: z.literal("decide"),
    challenge: text,
    side: z.enum(["lead", "peer"]),
    reason: text,
    answers: text,
    patch: Patch.optional(),
  }),
  z.object({ action: z.literal("escalate"), challenge: text, why: text }),
]);
export const AmendTask = z.object({ task: text, patch: Patch, reason: text });
export const Verdict = z.discriminatedUnion("action", [
  z.object({
    action: z.literal("approve"),
    task: text,
    sha: text,
    read: z.array(text),
    note: z.string().optional(),
    overFailed: z.string().optional(),
  }),
  z.object({
    action: z.literal("request_changes"),
    task: text,
    sha: text,
    findings: z.array(z.object({ path: text, line: z.number().int().optional(), problem: text })).min(1),
  }),
]);
export const Diff = z.object({ task: text, path: z.string().optional() });
export const Report = z.object({
  summary: text,
  approved: z.array(z.string()),
  open: z.array(z.string()),
  risks: z.array(z.string()),
});

// Peer
export const Propose = z.object({
  approach: text,
  steps: z.array(text).min(1),
  risks: z.array(text).min(1),
  assumptions: z.array(z.string()),
  questions: z.array(z.string()),
  requiredConcerns: z
    .array(z.object({ index: z.number().int().min(0), concern: text, evidence: z.array(Evidence) }))
    .optional(),
});
/** Some clients send booleans as "true"/"false" strings; accept both. */
function flag<V extends boolean>(value: V): z.ZodLiteral<V> {
  return z.preprocess((v) => (v === String(value) ? value : v), z.literal(value)) as unknown as z.ZodLiteral<V>;
}

export const Position = z.union([
  z.object({ agree: flag(true), comparison: z.array(Comparison).min(1) }),
  z.object({ agree: flag(false), challenges: z.array(Challenge).min(1) }),
]);
export const Rebut = z.object({ challenge: text, newEvidence: z.array(Evidence).min(1) });
export const Withdraw = z.object({ challenge: text, reason: text });
export const Handoff = z.object({
  outcome: z.enum(["complete", "partial"]),
  sha: text,
  summary: text,
  checks: z.array(CheckRun),
  skipped: z.array(z.string()),
  unknowns: z.array(z.string()),
  discovered: z.array(z.string()).optional(),
});

export const TOOL_SCHEMAS = {
  open_squad: OpenSquad,
  close_squad: CloseSquad,
  amend_squad: AmendSquad,
  status: Status,
  message: Message,
  ask: Ask,
  answer: Answer,
  plan: Plan,
  respond: Respond,
  amend_task: AmendTask,
  verdict: Verdict,
  diff: Diff,
  report: Report,
  propose: Propose,
  position: Position,
  rebut: Rebut,
  withdraw: Withdraw,
  handoff: Handoff,
} as const;

export type ToolName = keyof typeof TOOL_SCHEMAS;
export type ToolArgs<T extends ToolName> = z.output<(typeof TOOL_SCHEMAS)[T]>;

export type SourceT = z.output<typeof Source>;
export type EvidenceT = z.output<typeof Evidence>;
export type BriefT = z.output<typeof Brief>;
export type LeadApproachT = z.output<typeof LeadApproach>;
export type PatchT = z.output<typeof Patch>;
export type ChallengeT = z.output<typeof Challenge>;

/** Zod issues turned into "path: message" lines an agent can act on. */
export function describeIssues(error: z.ZodError): string[] {
  return error.issues.map((issue) => `${issue.path.length ? issue.path.join(".") : "(gốc)"}: ${issue.message}`);
}

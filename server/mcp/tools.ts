import { z } from "zod";
import { TOOL_SCHEMAS, type ToolName } from "../../shared/protocol.ts";

const DESCRIPTIONS: Record<ToolName, string> = {
  open_squad: "Mở một squad: một mục tiêu, acceptance đo được, ràng buộc Human đã nói kèm nguồn, slug ngắn theo mảng việc như pricing (tab Lead thành s1 lead pricing, nhánh squad/s1-pricing). Desk tạo Lead chạy opus; model fable chỉ cho squad thật khó.",
  amend_squad: "Ghi quyết định giữa chừng của Human vào squad đang chạy: thêm ràng buộc có nguồn hoặc acceptance mới. Lead nhận message và dùng id ràng buộc mới làm nguồn trong brief.",
  close_squad: "Đóng squad sau khi Human đã merge nhánh squad: archive các HC, dọn worktree.",
  status: "Bức tranh rút gọn: squad, task, tranh luận và câu hỏi đang mở, thống kê tranh luận.",
  message: "Nhắn một HC trong cây của bạn. Id HC lấy từ status hoặc message.",
  ask: "Hỏi cấp trên. Peer phải kèm bestGuess và làm tiếp theo nó trong lúc chờ.",
  answer: "Trả lời một câu hỏi gửi cho bạn. Trả lời leo thang phải ghi source.",
  plan: "Giao task: mỗi brief có title là tên ngắn như format (tab Peer thành s1-t2 peer format, nhánh task/s1-t2-format), đề bài và cách làm của Lead (niêm phong tới khi Peer đề xuất). required chỉ chứa ràng buộc có nguồn thật. Peer chạy sonnet; model haiku chỉ cho task nhỏ, máy móc.",
  respond: "Trả lời một tranh luận: concede kèm patch sửa đúng chỗ bị phản biện, hold kèm phản chứng chưa ai nộp trong tranh luận đó, decide khi bế tắc (answers = id bằng chứng mới nhất của Peer), hoặc escalate.",
  amend_task: "Sửa brief của task chưa handoff khi không có tranh luận mở, ví dụ khi đề xuất của Peer hay hơn. Peer đã đề xuất thì phải position lại với brief mới.",
  verdict: "Approve hoặc request_changes task theo SHA đã qua CI. Approve phải liệt kê file đã đọc; CI fail thì ghi overFailed.",
  diff: "Xem diff của task từ base tới SHA đã qua CI.",
  report: "Báo squad xong. Server chạy lại checks trên nhánh squad rồi gửi Supervisor.",
  propose: "Nộp phương án của bạn trước khi thấy cách làm của Lead. Chỉ nộp một lần.",
  position: "Sau khi so hai phương án: agree kèm comparison từng điểm khác, hoặc challenges kèm bằng chứng và hậu quả.",
  rebut: "Phản bác lần nữa khi Lead giữ ý. Phải có bằng chứng mới.",
  withdraw: "Rút một phản biện.",
  handoff: "Nộp việc theo SHA đã commit, kèm output thật của checks. Server sẽ chạy lại.",
};

type JsonSchema = Record<string, unknown>;

/** Claude wants an object at the top of every input schema; a union becomes one object of all variant fields. */
function objectSchema(schema: JsonSchema): JsonSchema {
  delete schema.$schema;
  const variants = (schema.oneOf ?? schema.anyOf) as JsonSchema[] | undefined;
  if (!variants) return schema;
  const properties: Record<string, JsonSchema> = {};
  let required: string[] | undefined;
  for (const variant of variants) {
    for (const [key, value] of Object.entries((variant.properties ?? {}) as Record<string, JsonSchema>)) {
      const existing = properties[key];
      if (existing && "const" in existing && "const" in value) {
        properties[key] = { type: typeof value.const === "boolean" ? "boolean" : "string", enum: [existing.const, value.const] };
      } else if (existing?.enum && "const" in value) {
        properties[key] = { ...existing, enum: [...(existing.enum as unknown[]), value.const] };
      } else if (!existing) {
        properties[key] = value;
      }
    }
    const req = (variant.required ?? []) as string[];
    required = required ? required.filter((k) => req.includes(k)) : req;
  }
  return { type: "object", properties, required: required ?? [] };
}

export function toolDescriptors(names: string[]) {
  return names
    .filter((name): name is ToolName => name in TOOL_SCHEMAS)
    .map((name) => ({
      name,
      description: DESCRIPTIONS[name],
      inputSchema: objectSchema(z.toJSONSchema(TOOL_SCHEMAS[name], { io: "input" }) as JsonSchema),
    }));
}

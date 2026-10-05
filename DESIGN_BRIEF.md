# Thiết kế plugin Paseo `squad`

Bản thiết kế, chưa có code. Cập nhật ngày 2026-10-04.

## Vì sao có squad

Subagent của Claude Code không tranh luận được với Lead. Nó nhận brief do Lead viết, nhìn vấn đề qua
giả định của Lead, chạy một lượt rồi trả về. Nó không có lúc nào để nói "brief này sai" rồi chờ Lead trả
lời, và Lead cũng không bị buộc phải trả lời.

Squad tồn tại để Peer phản biện thật trước khi làm:

- Peer tự lập phương án trước khi thấy cách Lead định làm, để không bị neo.
- Peer đưa bằng chứng phản bác; Lead buộc phải nhận sai, hoặc giữ ý kèm phản chứng.
- Tranh luận có luật kết thúc, và bất đồng được ghi lại.

Mọi phần khác như worktree, merge, hỏi Human đều phục vụ vòng đó. Khi cân nhắc một tính năng, câu hỏi đầu
tiên là: nó có giúp tranh luận thật hơn không?

Core là khung Supervisor, Lead, Peer cộng vòng tranh luận. Phần thuộc giai đoạn sau được ghi rõ tại chỗ
và gom lại ở mục 15.

## Thuật ngữ

| Khái niệm | Nghĩa |
| --- | --- |
| Squad | Một nhóm làm một mục tiêu: một Lead và các Peer. Đếm theo repo: `s1`, `s2` |
| HC (headcount) | Một suất người trong squad, do một agent Paseo đảm nhận: Supervisor, Lead hoặc Peer. Vòng đời `open → filled → closed` |
| Task | Một phần việc giao cho đúng một Peer, mã `s1-t2` |
| Brief | Đề bài của task: objective, AC, `required`, `scope`, `checks` và cách làm của Lead |
| AC (acceptance criteria) | Điều kiện đo được để coi là xong, của squad hoặc của task |
| Constraint | Ràng buộc có nguồn: lời Human, mục tiêu squad hoặc luật dự án. Mã `s1-c1` |
| Scope | Các glob file Peer được sửa |
| Checks | Lệnh phải exit 0 khi task xong |
| Debate | Tranh luận về brief hay cách làm: challenge, hold, rebut, concede, withdraw, decide, escalate. Mã `s1-t2-d1` |
| Evidence | Bằng chứng kèm tranh luận: đoạn file, output lệnh, tài liệu. Mã `s1-t2-e1` |
| Ask | Câu hỏi lên cấp trên. Mã `s1-a1` |
| Blocker | Premise sai Peer phát hiện giữa chừng, Lead `triage`. Mã `s1-t2-b1` |
| Stalled | Peer kết thúc lượt mà không nộp, không hỏi |
| Handoff | Peer nộp việc ở một SHA |
| CI | Server chạy lại `checks` trên SHA đã gộp nhánh squad |
| Verdict | Lead `approve` hoặc `request_changes` theo SHA đã qua CI |
| Message | Tin desk hay HC khác gửi tới một HC, kết bằng dòng `Next:` |

Desk (bộ luật kiểm mọi lệnh) và ledger (sổ ghi trạng thái) là tên trong code; agent không thấy hai khái niệm này.

## 1. Quyết định chính

| # | Chủ đề | Quyết định |
| - | --- | --- |
| D1 | Harness | Claude Code. Harness mô tả bằng dữ liệu để thêm Codex, OpenCode mà không sửa lõi |
| D2 | Vai trò | Đúng 3 vai trò: Supervisor, Lead, Peer. Mỗi vai trò một provider, chỉ chạy model của vai trò: Supervisor sonnet, Lead opus hoặc fable, Peer sonnet hoặc haiku |
| D3 | Peer | Peer là người làm việc thật, mặc định làm task. Loại HC khác là biến thể của Peer, khai `like: "peer"` trong `roles.json`: core chưa có biến thể nào; `reviewer`, `scout`, `architect` ở giai đoạn sau. Thêm biến thể không thêm vai trò hay provider |
| D4 | Quy mô | Mỗi workspace có 1 Supervisor và nhiều squad. Mỗi squad do một Lead dẫn |
| D5 | Human | Chỉ nói chuyện với Supervisor. Core hỏi Human bằng chat thường; giai đoạn sau có thẻ hỏi và pill |
| D6 | Giao thức | Server kiểm schema của brief, đề xuất, phản biện, handoff, verdict. Thiếu trường là bị từ chối |
| D7 | Lúc tranh luận | Trước khi làm. Peer phải đồng ý hoặc phản biện thì mới được handoff |
| D8 | Chống neo | Peer tự lập phương án trước khi thấy cách làm của Lead |
| D9 | Bế tắc | Tối đa 2 vòng, mỗi vòng phải có bằng chứng mới. Hết vòng thì Lead quyết kèm lý do, bất đồng được ghi lại. Tranh chấp về phạm vi sản phẩm lên Supervisor |
| D10 | Trong lúc tranh luận | Peer làm tiếp phần không bị tranh chấp. Không handoff khi còn tranh luận mở |
| D11 | Đồng ý có lập luận | Peer đồng ý phải so từng điểm khác giữa hai phương án và nói vì sao chấp nhận cách của Lead |
| D12 | Vấn đề giữa chừng | Peer báo `blocked` kèm bằng chứng. Lead đổi hướng thì task quay về vòng đề xuất |
| D13 | CI | Khi handoff, plugin gộp nhánh squad vào nhánh task rồi chạy lại `checks` trên SHA đã gộp, gửi output thật cho Lead |
| D14 | Review | Giai đoạn sau. Core chỉ có Lead đọc diff bằng `diff` trước khi `approve` và phải liệt kê file đã đọc |
| D15 | Git | Plugin quản lý worktree, nhánh, merge. Lead approve theo SHA thì plugin fast-forward nhánh squad. Human merge nhánh squad lên main |
| D16 | Worktree | Tổ chức như seatworks: mỗi việc (Lead của squad, task) một working copy riêng dưới thư mục của squad, với một workspace riêng dưới project của repo; agent tạo với `parent` là HC gọi nó, nên Paseo hiện cây Supervisor → Lead → Peer. Supervisor ở workspace của Human |
| D17 | Kênh tool | MCP HTTP trên `127.0.0.1`, cổng cố định chỉnh được, mỗi HC một secret trong URL |
| D18 | Tool được phép | Mỗi vai trò và mỗi peer khai rõ tool được phép; desk ép lúc tạo agent. Mẫu lệnh chỉ dùng để cấm |
| D19 | Tin nhắn | Không chen vào lượt đang chạy. Tin chờ hết lượt, hoặc đi kèm câu trả lời tool kế tiếp |
| D20 | Phạm vi sửa | Brief khai path được sửa. Server cảnh báo khi diff vượt phạm vi |
| D21 | Thứ tự task | Lead giao task phụ thuộc sau khi task nó cần đã merged |
| D22 | Khôi phục | Ledger và hàng đợi tin trên đĩa. Sau restart, nhận lại agent theo label |
| D23 | Kết thúc squad | Lead báo xong, Human merge lên main, plugin archive các HC, dọn worktree, giữ ledger |
| D24 | Stalled | Peer kết thúc lượt mà không nộp, không hỏi, không tranh luận thì desk nhắc một lần, lần sau báo Lead |
| D25 | Quyền Supervisor | Giai đoạn sau: Supervisor chỉ quyết loại việc Human cấp mã `S#`, server kiểm mã |
| D26 | Giám sát | Giai đoạn sau: luật tự động phía server, không dùng model |

Giả định còn mở:

- Peer `ask` thì vẫn làm tiếp theo phỏng đoán của mình trong lúc chờ.
- Hook `agent.session_open` dùng để đưa shim git lên đầu `PATH` mỗi lần mở agent.
- Danh tính commit của Peer: repo không có `user.name` thì Peer tự đặt bằng `-c`. Có thể cần plugin đặt sẵn danh tính cho worktree.
- Cấu hình agent, gồm `options`, được Paseo lưu và nạp lại khi resume. Đã đọc trong code Paseo, chưa chạy
  thử qua một lần restart daemon.

## 2. Vai trò

```text
Human
  │  chỉ nói chuyện với Supervisor
  ▼
Supervisor            1 mỗi workspace
  ├──────────────┬──────────────┐
  ▼              ▼              ▼
Lead A         Lead B         Lead C          mỗi Lead dẫn một squad, mỗi squad một mục tiêu
  └── Peer ×n     mỗi Peer làm một task
```

| Vai trò | Được làm | Không được làm | Ai chấm |
| --- | --- | --- | --- |
| Supervisor | Mở, đóng squad; nhắn Lead; trả lời câu hỏi và leo thang của Lead; xem trạng thái; hỏi Human | Sửa code, tạo agent, giao việc cho Peer, approve việc của Peer | Human |
| Lead | Chia mục tiêu thành brief, chọn peer cho từng brief, tranh luận với Peer, approve hoặc request_changes theo SHA, hỏi Supervisor | Sửa code, chạy lệnh shell, merge hay push bằng tay, nói chuyện với Human | Supervisor theo dõi, Human duyệt khi lên main |
| Peer | Làm việc thật trong giới hạn của peer được chọn: đề xuất, phản biện, sửa code; hỏi Lead | Push, checkout, merge, ra khỏi worktree của mình, tạo agent, nhắn Peer khác hay Supervisor | Lead |

Nguyên tắc:

- Ranh giới thật là ai chấm ai. Được làm không có nghĩa là được quyết.
- Quyền quyết đọc từ người gửi do server gán, không đọc từ lời văn trong tin.
- Phản biện là quyền, không phải lỗi. Prompt của Peer nói rõ: thấy brief sai mà không phản biện là làm sai
  việc. Lead không được trả lời "vì brief bảo thế"; server đòi phản chứng.

## 3. Kiến trúc

```text
┌───────────────────────── Paseo daemon ─────────────────────────────┐
│  plugin squad, index.server.ts                                      │
│   ├─ Host         giữ Paseo API nhận từ hook, event đầu tiên        │
│   ├─ Desk         lõi nghiệp vụ: squad, task, tranh luận, verdict   │
│   ├─ Ledger       trạng thái trên đĩa, transact đồng bộ             │
│   ├─ Outbox       hàng đợi tin, giao khi hết lượt hoặc kèm tool     │
│   ├─ Git          worktree, gộp nhánh, chạy git an toàn             │
│   ├─ MCP HTTP     127.0.0.1:<port>/mcp/<secret>, phân tool theo HC │
│   └─ Hooks        before agent.create, session_open; event lượt     │
│  agent Claude Code ──HTTP──▶ MCP HTTP của plugin                    │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.1 Paseo API, provider và cách tạo HC

Plugin chỉ chạy khi `pluginsEnabled: true` trong config Paseo; Human bật một lần khi cài.

Paseo chỉ đưa API cho plugin qua hook, event hoặc RPC; lúc plugin khởi động chưa có API. Host giữ API
từ hook hoặc event đầu tiên của bất kỳ agent nào, như `session_open` hay `turn_started`, và API đó vẫn dùng
được sau khi hook trả về. Có API rồi plugin mới đồng bộ provider, nhận lại agent và giao message tồn. Tool
squad gọi tới trước lúc đó nhận "squad đang khởi động, gọi lại sau". Reload plugin làm mất API cho tới
event kế tiếp. Provider đã ghi vào config thì còn qua các lần restart, nên chỉ lần cài đầu cần một hoạt
động agent bất kỳ để "Squad · Supervisor" hiện ra.

Mỗi vai trò một provider theo harness, `squad-<harness>-<vai trò>`: `squad-claude-supervisor`,
`squad-claude-lead`, `squad-claude-peer`. Khi có API, plugin đọc config rồi dùng `config.patch` ghi đè 3
entry này; khi gỡ thì xóa đúng 3 ID đó.

```text
entry = { extends: "claude", label: "Squad · Lead",
          command: ["node", "<state>/bin/hc-launcher.mjs", "<claude.exe>"],
          env: { SQUAD_ROLE: "lead", SQUAD_GRANT: "" },
          models: [{ id: "claude-opus-5-5", isDefault: true }, { id: "claude-fable-5-1" }],
          disallowedTools: ["Agent", "Task", "AskUserQuestion", "SendMessage"],
          paseoTools: { enabled: false } }      ← không HC nào có tool của Paseo
```

Provider cho phép tắt tool Paseo ở mức chắc nhất, cho Human chọn "Squad · Supervisor" ngay trong Paseo,
và cho hook biết vai trò từ tên provider. `models` thay hẳn danh sách model của Claude, nên Paseo chỉ cho
chọn model của vai trò. `disallowedTools` cấm tool nguy hiểm ngay ở provider; danh sách tool được phép vẫn
đi theo từng HC vì mỗi peer một khác.

| Vai trò | Model | Ai chọn |
| --- | --- | --- |
| Supervisor | sonnet | Cố định. Hook ép về sonnet dù request chọn model khác |
| Lead | opus, fable | Mặc định opus. Supervisor ghi `model: fable` trong `open_squad` cho squad thật khó |
| Peer | sonnet, haiku | Mặc định theo file peer. Lead ghi `model: haiku` trong brief cho task nhỏ, máy móc |

Danh sách nằm ở `models:` của 3 file vai trò, model đầu là mặc định. Tên ngắn đổi ra id model qua
`server/harness/claude/harness.json`. Desk từ chối model ngoài danh sách với mã `bad_model`.

- Supervisor do Human tạo bằng provider "Squad · Supervisor" trong workspace của repo. Mỗi workspace có một
  Supervisor còn sống; tạo thêm bị hook từ chối.
- Lead và Peer do desk tạo bằng `workspaces.ref(ws).agents.create`, với label `squad.ws`, `squad.id`,
  `squad.role`, `squad.kind`, `squad.task`, `squad.HC`. Biến thể của Peer sau này dùng chung `squad-claude-peer`.
- Desk ghi HC vào ledger trước khi gọi `agents.create`, truyền `idempotencyKey = squad:<HC>`. Gửi lại
  cùng key với request khác thì Paseo trả `agent_request_key_conflict`. Nếu `create` lỗi hoặc timeout, desk
  tìm agent theo label trước khi tạo lại, nên không bao giờ có hai agent cho một HC.
- Hook `agent.create` cũng chạy với agent do desk tạo, nên hook phải cho HC desk đã đăng ký đi qua.

| Hook | Việc |
| --- | --- |
| `agent.create` | Chỉ xử lý provider `squad-*`. Lead và Peer phải có HC chờ do desk đăng ký, nếu không thì từ chối, nên không ai tạo HC squad bằng tay. Supervisor: nếu workspace chưa có thì cấp secret, gắn prompt, MCP và tool được phép theo mục `supervisor` của `roles.json` |
| `agent.session_open` | Chạy mọi lần mở, kể cả resume. Cấp `SQUAD_GRANT` và đưa shim lên đầu `PATH`, vì Paseo không lưu env |

Nối agent với HC: Lead và Peer lấy `agentId` từ `agents.create`. Supervisor được nối qua event
`agent.created` khi workspace đang có HC Supervisor chờ.

HC không bao giờ chạy thiếu vai trò. Provider squad khởi động Claude qua launcher của squad. Lệnh đầu phải
là `"node"`: Paseo chạy nó bằng Node của chính Paseo, còn đường dẫn `Paseo.exe` thì Paseo gỡ cờ chạy Node và
nó mở ra Paseo CLI. Launcher thấy
`PASEO_AGENT_ID` mà không có `SQUAD_GRANT`, tức hook `session_open` của squad không chạy vì plugin tắt, hỏng
hay đã gỡ, thì thoát với mã 2 và báo lý do. Paseo dò binary không có `PASEO_AGENT_ID` nên đi qua. Launcher
nối Claude bằng pipe, nên khi Paseo kill launcher thì Claude mất stdin và tự thoát.

### 3.2 Vai trò, prompt và tool được phép

Tổ chức như seatworks, ba chỗ:

```text
roles.json                                      danh sách loại HC: supervisor, lead, peer
content/prompts/supervisor.md lead.md peer.md
                                                prompt, chỉ có văn bản; skill theo vai trò sau này ở content/skills/
server/harness/claude/
  harness.json                                  binary, mode, tên tool hợp lệ, cờ chung của HC, bảng model
  settings.json                                 permissions.deny chung cho mọi HC
  settings/<loại HC>.settings.json             permissions.allow và deny riêng của loại HC
```

Chỉ có 3 vai trò, cũng là 3 provider. Peer mặc định là người làm task: Lead `plan` thì task nào cũng giao
cho một Peer mới, không phải chọn, và task nào cũng qua vòng đề xuất và tranh luận ở mục 5. Loại HC khác là biến thể của Peer, khai `like: "peer"`, chạy trên
provider Peer với prompt, tool và settings riêng. Core chưa có biến thể nào; `reviewer`, `scout`, `architect` ở giai đoạn sau. Thêm biến thể là thêm một mục trong `roles.json`, một prompt và
một file settings, ví dụ:

```json
{ "role": "scout", "like": "peer", "label": "Scout",
  "description": "A Peer that reads the repo and reports, changing nothing",
  "models": ["sonnet"], "squadTools": ["ask"],
  "prompt": "content/prompts/scout.md" }
```

Trường của một mục: `models` (model đầu là mặc định; biến thể chỉ chọn trong models của Peer vì provider
Peer chỉ có các model đó), `squadTools`, `prompt`, `hidesWords` (chữ prompt không được chứa; biến thể thừa
hưởng của Peer). `label` đặt tên provider, `description` chỉ để người đọc.

`permissions.allow` trong settings là các tool duy nhất HC có, chỉ ghi tên tool nguyên vẹn.
`permissions.deny` ghi mẫu bị cấm bên trong tool đã cho phép, ví dụ `Bash(git push:*)`; deny của
`settings.json` áp cho mọi HC, và tên tool trơn trong đó là tool không HC nào được cấp. Tool squad của
từng vai trò xem mục 7.

Tên tool phải có trong `knownTools` của harness; gõ sai như `Webfetch` thì plugin không nạp, vì agent sẽ
lặng lẽ bỏ qua tên lạ. Sửa file xong chỉ cần `npm test` hoặc `npm run typecheck`: hai lệnh này tự nhúng lại
`roles.json`, `content/` và `server/harness/` vào code. HC đang chạy giữ tool cũ, HC tạo sau mới có tool mới.

| Loại HC | `allow` | `deny` riêng |
| --- | --- | --- |
| Supervisor | `Read`, `Grep`, `Glob`, `TaskCreate`, `TaskUpdate`, `TaskList`, `TaskGet` | |
| Lead | `Read`, `Grep`, `Glob`, `TaskCreate`, `TaskUpdate`, `TaskList`, `TaskGet`. Đọc diff bằng tool squad `diff` | |
| Peer | `Read`, `Grep`, `Glob`, `Edit`, `Write`, `NotebookEdit`, `TaskCreate`, `TaskUpdate`, `TaskList`, `TaskGet`, `Bash` | `Bash(git push:*)`, `Bash(git pull:*)`, `Bash(git fetch:*)`, `Bash(git checkout:*)`, `Bash(git switch:*)`, `Bash(git merge:*)`, `Bash(git rebase:*)`, `Bash(git reset --hard:*)`, `Bash(git worktree:*)` |

Danh sách việc cần làm dùng nhóm `Task*`: Claude Code 2.1.285 chạy qua Paseo không còn `TodoWrite`, ghi
`TodoWrite` trong `allow` thì HC chỉ không có tool đó. Đã kiểm trên Paseo thật.

Desk ép danh sách với Claude:

- `allow` vào `options.extraArgs.tools` dạng `"Read,Grep,Glob,Bash"`. Đây là danh sách cho phép thật của
  Claude Code: tool ngoài danh sách không tồn tại trong phiên, kể cả `AskUserQuestion`, `SendMessage`,
  `Skill`, `Workflow`, `Cron*`, `PowerShell` trên Windows, và cả tool Claude Code thêm ở bản sau. Không cần
  liệt kê tool để chặn.
- `allow` cũng vào `options.allowedTools`, nên chạy không cần hỏi.
- HC được cấp đúng các tool trong `allow`. Mẫu `deny` viết cho `Bash`, nên tool shell nào khác được cấp, như `PowerShell` trên Windows, sẽ không bị các mẫu đó chặn; khi ấy chỉ còn git shim chống nhầm.
- Mẫu trong `deny` vào `options.settings.permissions.deny`. Rule deny luôn thắng rule allow, kể cả allow
  trong `~/.claude/settings.json` của Human, nên settings của Human không nới được quyền của HC.
- `hcArgs` trong `harness.json` vào `options.extraArgs` của mọi HC: `strict-mcp-config` để HC chỉ thấy server
  MCP `squad`, và `setting-sources: project` để HC chỉ nạp settings của repo. Hook, luật permission,
  `CLAUDE.md` và plugin Claude trong `~/.claude` của Human không vào HC squad.
- Desk từ chối mọi yêu cầu permission của HC squad qua event `agent.permission_requested`, bằng
  `respondToPermission` với `behavior: "deny"` và lời nhắc tool được phép và dùng `ask`; agent nhận nguyên
  văn lời nhắc đó. Đây là lưới chặn cuối; HC chạy ở mode `default`, không bao giờ ở mode bỏ qua permission.
- Mọi agent bị cấm đọc ghi `<state>/workspaces/**`, `<state>/config.json` và `~/.paseo` bằng mẫu `deny`
  chung trong harness. Worktree dưới `<state>/worktrees` không bị cấm.
- Đăng nhập không nằm trong settings nên HC vẫn dùng tài khoản Claude của Human. Thư mục config riêng mỗi
  HC như seatworks không dùng được trên Windows vì token đăng nhập bị xoay vòng.
- Plugin kiểm mọi file lúc khởi động và không nhận tool cấm hay tên tool lạ trong `allow`, vai trò thứ tư,
  model ngoài provider, tool squad không có thật, hay prompt chứa chữ bị giấu.

Desk truyền vào `agents.create`, và Paseo lưu lại nên vẫn còn khi resume: `systemPrompt` là prompt của loại
HC, còn đề bài là tin đầu tiên; `modeId`, model; `mcpServers.squad = { type: "http", url }`;
`toolPolicy.preapproved` cho tool squad; `options` tính từ settings của loại HC.

### 3.3 Chỗ làm việc

| HC | Thư mục làm việc |
| --- | --- |
| Supervisor | Workspace Human chọn khi tạo. Không có tool sửa |
| Lead | Working copy riêng `<state>/worktrees/<project>/s1-pricing/lead`, tách khỏi nhánh, ở đầu nhánh squad. Không có tool sửa. Plugin đưa working copy này lên commit mới sau mỗi lần merge, nên Lead luôn đọc đúng code của squad. Đọc diff bằng `diff` |
| Peer | Working copy riêng `s1-pricing/s1-t2-format`, trên nhánh task của squad, tạo từ đầu nhánh squad |

Tổ chức giống hệt seatworks:

- `<project>` là tên thư mục repo viết thường cộng 6 ký tự hash của đường dẫn, ví dụ `money-1a2b3c`.
- Mỗi việc lấy một working copy dưới `<state>/worktrees/<project>/<squad>/`, bị `git worktree lock` trong lúc
  dùng. Mỗi working copy có một workspace Paseo tạo dưới project của repo, nên không sinh project mới.
- Khi việc xong, working copy bị xoá và workspace bị archive. Working copy còn thay đổi chưa commit là bản duy
  nhất của việc đó, nên được mở khoá và giữ lại cho Human; Supervisor nhận message báo đường dẫn.
- Agent tạo với `parent` là HC đã gọi nó: Lead là con của Supervisor, Peer là con của Lead. Paseo
  hiện cây đó, và agent có cha không gửi thông báo cần chú ý cho Human; chỉ Supervisor gửi.

Đặt tên theo seatworks, với khái niệm của squad: squad thay cho lane, mã nối bằng dấu `-`, tiêu đề cách bằng dấu
cách thay cho `·`, không dùng dấu chấm. Mọi tên viết thường. Squad đếm theo repo; mọi thứ bên trong đếm lại từ 1
trong cấp cha và mang mã cha ở đầu, nên nhìn một mã là biết thuộc squad và task nào. Tiêu đề ghi mã, vai trò rồi
việc đang làm, nên nhìn tab là biết ai làm gì.

| Thứ | Mã | Tiêu đề agent và workspace | Nhánh | Thư mục dưới `<project>/` |
| --- | --- | --- | --- | --- |
| Supervisor | `supervisor`, Supervisor thay thế ngồi lại đúng HC này | `supervisor`, trừ khi người tạo đã đặt | | workspace của Human |
| Squad và Lead | `s1`, HC `s1-lead` | `s1 lead pricing` | `squad/s1-pricing` | `s1-pricing/lead` |
| Task và Peer làm nó | `s1-t2` | `s1-t2 peer format` | `task/s1-t2-format` | `s1-pricing/s1-t2-format` |
| Tranh luận, bằng chứng, blocker | `s1-t2-d1`, `s1-t2-e1`, `s1-t2-b1` | | | |
| Câu hỏi, ràng buộc | `s1-a1`, `s1-c1` | | | |
| Tác giả commit | | `squad s1-t2` | | |

Ví dụ sidebar của một repo chạy hai squad:

```text
supervisor
s1 lead pricing
s1-t1 peer format
s1-t2 peer stock-immutable
s2 lead checkout-ui
s2-t1 peer cart-total
```

Luật desk kiểm:

- Slug của squad và tên task là tên ngắn: chữ thường nối bằng `-`, slug tối đa 20 ký tự, tên task tối đa 24, ví
  dụ `pricing`, `stock-immutable`. Chữ hoa và khoảng trắng được đổi giúp. Tên không được mở đầu giống một mã
  (`t2`, `d1`, `s3-x`) hay tên HC (`lead`, `peer`, `supervisor`), để tiêu đề không đọc nhầm thành mã khác.
- Số đã dùng không dùng lại, kể cả khi squad đóng hay task bị cắt. Số squad đếm trong workspace nhưng nhánh nằm
  chung một repo, nên `open_squad` bỏ qua số đã có nhánh `squad/s<n>-…` hay `task/s<n>-…`, ví dụ do một workspace
  khác trên cùng repo. Nhánh task đã có thì `plan` bị từ chối với `branch_taken`.
- Hai squad đang chạy không được trùng slug.

Tên hiển thị không mang id workspace Paseo hay hash git, vì đã không trùng ở chỗ cần: mã là duy nhất trong một
repo nhờ đếm và bỏ qua số đã có nhánh, thư mục nằm dưới `<project>` có hash đường dẫn repo, và Paseo nhóm
workspace theo project. Mã HC thì lặp giữa các repo (repo nào cũng có `s1-lead`), nên khóa nội bộ luôn kèm
workspace: nhãn agent `squad.ws` cộng `squad.hc`, `idempotencyKey` là `squad:<ws>:<hc>`. Sau restart, desk
nhận lại agent theo agent id trước, rồi mới theo cặp nhãn đó.

Mỗi HC có workspace riêng, vì Paseo chạy agent trong thư mục của workspace còn mỗi Peer cần worktree riêng.
Workspace và agent cùng tiêu đề, và thư mục của một squad nằm chung dưới `<project>/s1-pricing/`. Trong squad
của mình, HC được viết mã tắt như `t2`, `t2-d1`, `lead`, `a1`; Supervisor viết mã đủ và gọi squad bằng
`s1`, `s1-pricing` hoặc `pricing`. Mã gửi lên không phân biệt hoa thường.

Brief khai `scope` là các glob được sửa. Khi handoff, server so `git diff --name-only base..sha` với `scope`
và cảnh báo Lead nếu vượt. Hai task chưa merge của một squad không bao giờ chung file: `plan` và `patch.scope`
có `scope` giao với task đang làm, hay với brief khác trong cùng lần plan, bị từ chối với `scope_overlap`. Hai
glob được coi là giao khi phần thư mục trước ký tự đại diện trùng nhau, nên luật nghiêng về phía chặn.

Mỗi workspace có tối đa `maxPeers` Peer đang làm, mặc định 4, tính chung mọi squad chưa đóng; `plan` vượt số đó bị
từ chối với `too_many_peers`. Lead bị từ chối thì kết thúc lượt; khi có task merge hoặc squad đóng làm trống chỗ,
desk gửi message `hc_free` cho Lead đó, vì ngoài message ra không có gì đánh thức nó. CI chạy tối đa `ciAtOnce` lượt cùng lúc trên máy, mặc định nửa số nhân CPU; lượt
sau chờ. Cả hai chỉnh trong `<state>/config.json`.

### 3.4 Danh tính và kênh tool

```text
claude ──POST http://127.0.0.1:<port>/mcp/<secret>──▶ secret → HC → vai trò và peer → tool set
```

- Cổng cố định, mặc định 6810, đổi được trong `<state>/config.json`, để URL đã lưu trong agent vẫn đúng sau restart. Cổng
  bị chiếm thì plugin không khởi động và báo lỗi rõ; Human đổi cổng rồi reload, plugin cập nhật
  `mcpServers.squad` của các agent đang có.
- Secret 24 byte ngẫu nhiên, ledger chỉ lưu hash. URL chứa secret không bao giờ ra log.
- Mọi tin mang `from` do server gán. Agent bị archive thì secret bị thu hồi, request nhận 401.
- MCP chỉ nhận POST JSON-RPC: `initialize`, `tools/list`, `tools/call`, `ping`. Claude còn gọi `server/discover` và mở GET để chờ stream; server trả 405 cho GET, nếu không Claude gọi lại mỗi giây.
- Tool chạy quá khoảng 4 phút thì server trả "đang làm, kết quả tới bằng message". Gọi lại cùng args thì nhập
  vào lần chạy đang dở.

### 3.5 Dữ liệu

```text
<state> = SQUAD_STATE, hoặc %LOCALAPPDATA%/squad trên Windows, ~/.local/share/squad nơi khác
  config.json                      cổng MCP, maxPeers, ciAtOnce
  workspaces/<ws-id>/
    ledger.json                    seq, squads, tasks, HCs, debates, asks, verdicts
    mailbox.json                   message đang chờ giao, và message đã gửi để nhận ra tin của Human
    events.jsonl                   nhật ký chỉ ghi thêm, cuộn theo kích thước
    project.json                   base branch, luật dự án
    handoffs/<task>.md  verdicts/<task>.md
  worktrees/<project>/s1-pricing/  lead/  s1-t2-format/  …   working copy của từng việc
```

- `transact()` đọc, quyết định, ghi đồng bộ, không `await` ở giữa; trong một tiến trình là đủ nhất quán.
- Ghi nguyên tử bằng file tạm rồi `rename`, quyền 0600. Ledger hỏng thì không bao giờ bị ghi đè; desk báo
  lỗi và ngừng nhận lệnh tới khi Human xử lý.
- Ghi ledger trước, gọi Paseo hay git sau. Bước ngoài lỗi thì ledger được đưa về trạng thái trước đó.

| Đối tượng | Trạng thái |
| --- | --- |
| HC | `open → filled → closed`: desk mở suất khi giao việc, có agent thì `filled`, xong việc hay mất agent thì `closed`. Riêng HC `supervisor` mất agent thì về `open`, câu hỏi và message cho nó chờ Supervisor mới |
| Task | `proposing → debating → running → ci_running → ci_done → approved → merged`. Nhánh phụ: `ci_running → running` khi gộp nhánh squad bị conflict, `request_changes → running`, `blocked → running` hoặc `→ proposing`, nhánh squad đi tiếp thì `ci_done → ci_running` |
| Tranh luận | `open → held → rebutted → held → deadlocked → decided`. Kết thúc sớm: `conceded`, `withdrawn`. Leo thang: `escalated → decided` theo câu trả lời của Supervisor |
| Squad | `open → ready → closed`. Giai đoạn sau thêm `held`, `landing`, `landed`, `dropped` |

## 4. Giao thức

Mọi schema viết bằng Zod trong `shared/protocol.ts`. Khi từ chối, server trả danh sách trường sai và cách
sửa.

### 4.1 Brief

Brief có hai nửa. Peer thấy đề bài ngay; cách làm của Lead được niêm phong tới khi Peer nộp phương án.

```ts
Brief = {
  title: string,
  // Đề bài: Peer thấy ngay
  objective: string,                         // một câu, kết quả đo được
  acceptance: string[],                      // AC: ít nhất 1, đo được
  required: { text: string, source: Source }[],   // ràng buộc bắt buộc, phải có nguồn
  unknowns: string[],
  scope: string[],                           // glob được sửa
  reads?: string[],
  checks: string[],                          // lệnh Peer phải chạy; CI chạy lại khi handoff
  // Cách làm của Lead: niêm phong
  leadApproach: {
    approach: string,
    steps: { id: string, text: string }[],
    current: { id: string, text: string }[], // lựa chọn đang dùng, Peer được chất vấn
    risks: string[],
  },
  model?: string,
}
Source = { kind: "human" | "squad-goal" | "project-rule", ref: string }
```

Mỗi mục `required` phải trỏ tới thứ có thật: một câu Human đã nói, mục tiêu squad, hoặc một dòng luật dự
án. Không có nguồn thì server từ chối và gợi ý chuyển sang `leadApproach.current`. Đây là chỗ chặn Lead
biến lựa chọn của mình thành "luật" để né tranh luận.

### 4.2 Handoff

```ts
Handoff = {
  outcome: "complete" | "partial",           // không làm tiếp được thì dùng blocked
  sha: string,
  summary: string,
  checks: { command: string, exit: number, output: string }[],   // Peer khai, cắt còn 4 KB
  skipped: string[],                         // lệnh cố tình không chạy
  unknowns: string[],                        // giả định còn đứng, điều cần người khác quyết
  discovered?: string[],                     // việc mới ngoài phạm vi
}
```

- Từ chối nếu chưa qua vòng đề xuất, còn tranh luận mở, hoặc worktree còn thay đổi chưa commit.
- `sha` phải là HEAD của nhánh task và có `base` là tổ tiên. So file đổi với `scope`.
- Peer đang làm theo quyết định mình phản đối thì server tự thêm bất đồng đó vào `unknowns`.
- Tool trả ngay "đã nhận, CI đang chạy", task sang `ci_running`. Từ đây tới verdict, Peer không được sửa
  worktree; HEAD đổi thì kết quả CI bị hủy.
- Nếu đầu nhánh squad chưa nằm trong nhánh task, plugin chạy `git merge squad/s1-pricing` trong worktree của
  task. Có conflict thì plugin để lại dấu conflict, task về `running` kèm danh sách file;
  Peer gỡ, commit, handoff lại. Peer không tự chạy `git merge`.
- CI: server chạy lại từng lệnh `checks` trên SHA đã gộp, trong bash như HC dùng, kể cả trên Windows qua Git Bash; mỗi lệnh tối đa 10 phút mặc định. Xong thì task
  sang `ci_done`; message gửi Lead có SHA đã gộp, output Peer khai cạnh output thật, đánh dấu lệnh lệch exit
  code. Lệnh fail thì Peer cũng nhận message.

Nhờ gộp trước khi chạy CI, `checks` luôn chạy trên đúng code sẽ vào nhánh squad, và hai task song song
làm hỏng nhau sẽ lộ ra ở task nộp sau.

### 4.3 Verdict

```ts
Verdict =
  | { action: "approve", task: string, sha: string, read: string[], note?: string, overFailed?: string }
  | { action: "request_changes", task: string, sha: string, findings: { path: string, line?: number, problem: string }[] }
```

- `sha` là SHA đã gộp trong message `ci_done`, và HEAD của worktree vẫn ở đó. Task đã `ci_done`.
- Approve phải có `read` không rỗng. Có lệnh fail khi CI chạy lại thì phải ghi lý do vào `overFailed`;
  lý do này hiện trong thống kê `status`.
- Sau approve, plugin kiểm đầu nhánh squad là tổ tiên của `sha` rồi `update-ref` nhánh squad lên `sha`, không
  tạo commit merge. Nếu nhánh squad đã đi tiếp từ lúc chạy CI vì task khác vừa merge, task tự về `ci_running`,
  gộp lại, chạy lại CI, rồi Lead nhận message `ci_done` mới.
- Sau request_changes, task về `running` với cùng HC và worktree.

### 4.4 Câu hỏi, blocker, stalled

- `ask{question, bestGuess}`: Peer hỏi Lead, làm tiếp theo `bestGuess` trong lúc chờ. Lead hỏi Supervisor
  bằng `ask{question}`. Bên được hỏi trả lời bằng `answer{ask, text}`.
- `blocked{target, claim, evidence, impact}`: đang làm thì phát hiện premise sai; desk ghi một blocker `s1-t2-b1`. Cùng schema và cách kiểm
  như một điểm phản biện ở mục 5.3. Peer dừng phần bị ảnh hưởng.
- Lead trả lời mỗi `blocked` bằng `triage{blocker, decision, reason, patch?, counterEvidence?}`:
  - `change` kèm `patch` sửa đúng chỗ bị báo, như `concede`: task về `proposing` với brief mới, Peer đi lại
    vòng đề xuất cho phần đã đổi;
  - `keep` kèm `counterEvidence`, cùng luật như `hold`: task về `running`, Peer chỉ có thể `ask`;
  - `escalate`: lên Supervisor như mục 5.4.
- Supervisor cần Human quyết thì hỏi trong chat của mình, rồi gọi `answer`.
- HC hỏi bị mất, ví dụ Human archive tay, thì câu hỏi đang chờ của nó thành `dropped`; message `hc_lost` báo
  Supervisor không cần trả lời nữa. Đóng squad vẫn archive workspace của HC đã mất.
- Stalled: ở `agent.turn_ended` của Peer, nếu task đang `proposing` hoặc `running` mà không có `ask`,
  tranh luận hay `blocked` nào đang mở và Peer chưa handoff, desk nhắc Peer một lần bằng message kèm `Next:`.
  Lượt sau vẫn vậy thì desk báo Lead kèm câu cuối của Peer. Lượt lỗi thì báo Lead ngay.

## 5. Tranh luận trước khi làm

Đây là lõi của squad, áp dụng cho mọi task của Peer.

### 5.1 Toàn bộ vòng

```text
Lead ─plan{brief}─▶ desk tạo Peer, chỉ đưa đề bài; leadApproach niêm phong
Peer đọc repo, tự nghĩ ─propose─▶ desk khóa đề xuất, mở niêm phong
      ├─▶ Peer nhận cách làm của Lead, đặt cạnh đề xuất của mình
      └─▶ Lead nhận đề xuất của Peer
Peer ─position{agree, comparison}─▶ task running
Peer ─position{challenges}─▶ mỗi điểm là một tranh luận
    Vòng 1:  Lead concede ─▶ brief mới, đóng
             Lead hold kèm phản chứng ─▶ Peer rebut với bằng chứng mới, hoặc withdraw
    Vòng 2:  Lead concede hoặc hold
    Vẫn giữ ─▶ deadlocked ─▶ Lead decide kèm lý do ─▶ ghi bất đồng
Peer làm theo quyết định; bất đồng tự vào handoff
```

### 5.2 Đề xuất mù

Peer chỉ thấy `objective`, `acceptance`, `required`, `unknowns`, `scope`, `checks`. Message giao việc nói
rõ: lập phương án trước, chưa sửa code.

```ts
Propose = {
  approach: string,
  steps: string[],
  risks: string[],              // ít nhất 1
  assumptions: string[],
  questions: string[],
  requiredConcerns?: { index: number, concern: string, evidence: Evidence[] }[],
}
```

- Server lưu đề xuất kèm hash; Peer không sửa được nữa, nên không thể đồng ý sau khi xem đáp án.
- Câu trả lời của `propose` là cách làm của Lead đặt cạnh đề xuất của Peer, kèm lời dặn: điểm nào có bằng
  chứng làm đổi kết quả thì phản biện. Lead thấy đề xuất của Peer hay hơn thì `concede` khi Peer phản biện,
  hoặc `amend_task` khi Peer đã đồng ý.
- Commit trước `propose` không bị chặn cứng, nhưng handoff bị chặn tới khi vòng xong.
- `requiredConcerns` là lo ngại về ràng buộc bắt buộc. Lead không có quyền đổi chúng, nên server tự chuyển
  thành câu hỏi của Lead lên Supervisor.

### 5.3 Phản biện

```ts
Position   = { agree: true, comparison: Comparison[] } | { agree: false, challenges: Challenge[] }
Comparison = { point: string, mine: string, lead: string, whyLeadIsFine: string }   // ít nhất 1
Challenge  = {
  target: { kind: "approach" | "step" | "current" | "acceptance" | "scope", ref?: string },
  claim: string,            // điều gì trong cách của Lead là sai
  evidence: Evidence[],     // ít nhất 1
  impact: string,           // không đổi thì hậu quả cụ thể là gì
  proposal: string,         // đổi thành gì
  affects: string[],        // bước hoặc path tạm khóa trong lúc tranh luận
}
Evidence   = { kind: "file", path: string, lines?: [number, number], quote?: string }
           | { kind: "command", command: string, exit: number, output: string }
           | { kind: "doc", ref: string, quote: string }
```

- Đồng ý: `comparison` có ít nhất một mục, mọi trường không rỗng. Hai phương án giống nhau thì vẫn ghi một
  mục nói rõ điều đó.
- Phản biện: có bằng chứng; `impact` không rỗng, để lọc phản biện thuần khẩu vị; `target.ref` trỏ tới bước
  hoặc lựa chọn có thật; bằng chứng `file` có path tồn tại ở `base`, dòng nằm trong file, và `quote` nếu có
  phải nằm trong các dòng đó, bỏ qua khác biệt khoảng trắng. Luật này áp cho bằng chứng của cả Lead lẫn Peer.
  Bằng chứng `command` ghi nhận như bên nộp khai.

### 5.4 Luật tranh luận

Lead trả lời bằng `respond{challenge, action, …}`.

| Bước | Ai | Gọi | Server kiểm |
| --- | --- | --- | --- |
| Nhận sai | Lead | `respond{concede, patch}` | `patch` tạo brief mới và phải sửa đúng chỗ bị phản biện: bước hay lựa chọn `ref` bị bỏ hoặc viết lại, `approach` thì `leadApproach` đổi, `acceptance` hay `scope` thì trường đó đổi. Patch không đổi gì bị từ chối. Đụng mục tiêu squad thì phải `escalate` |
| Giữ ý | Lead | `respond{hold, counterEvidence, reason}` | Có ít nhất một phản chứng chưa ai nộp trong tranh luận này, như luật của `rebut`, và không chỉ trỏ lại chính brief. Không có luật này thì Lead nộp lại cùng phản chứng hai lần là tới `decide` |
| Phản bác | Peer | `rebut{challenge, newEvidence}` | Có bằng chứng mới. Sửa vài chữ trong câu trích không làm bằng chứng thành mới: khoảng dòng nằm trong khoảng đã nộp của cùng file, cùng lệnh, hay cùng đoạn tài liệu đều tính là cũ. Luật này áp cả cho `hold` |
| Rút | Peer | `withdraw{challenge, reason}` | |
| Bế tắc | Desk | | `hold` lần hai đưa tranh luận sang `deadlocked`; Lead không `hold` được nữa, Peer không `rebut` được nữa nhưng vẫn `withdraw` được |
| Quyết | Lead | `respond{decide, side, reason, answers, patch?}` | Chỉ khi `deadlocked`. `side` là `lead` hoặc `peer`; theo Peer thì kèm `patch`. `answers` phải là id bằng chứng mới nhất của Peer, để `reason` buộc phải đáp lại nó. Ghi vào ledger và `status` |
| Leo thang | Lead | `respond{escalate, why}` | Khi đụng phạm vi sản phẩm, chi phí lớn, tác động ra ngoài, thứ không đảo ngược được. Supervisor trả lời bằng `answer`, có thể sau khi hỏi Human; câu trả lời đóng tranh luận và thành nguồn `human` hoặc `squad-goal` |
| Sau quyết định | Peer | | Làm theo, không có vòng 3. Thấy làm tiếp không an toàn thì `blocked` kèm bằng chứng mới |

### 5.5 Trong lúc tranh luận và ghi lại

- Bước và path trong `affects` coi như khóa; Peer làm tiếp phần còn lại. Mọi bước bị khóa thì Peer kết thúc
  lượt, message của Lead sẽ đánh thức lại. Lượt này không tính là stalled.
- `handoff` bị từ chối khi còn tranh luận chưa ở `conceded`, `withdrawn` hoặc `decided`.
- Brief đổi mà một tranh luận khác đang trỏ tới bước hay lựa chọn không còn nữa thì Peer nhận message kèm brief
  mới. Tranh luận vẫn mở: brief mới đã giải quyết thì Peer `withdraw`, chưa thì Lead vẫn phải trả lời.
- `amend_task{task, patch, reason}` cho Lead sửa brief ngoài tranh luận, khi task chưa handoff và không còn
  tranh luận mở. Trước `propose` cách làm vẫn niêm phong, Peer chỉ nhận phần đề bài đổi. Sau `propose` task về
  `debating`, Peer phải `position` lại với brief mới, nên Lead không lách được vòng phản biện bằng cách sửa
  brief sau khi Peer đồng ý.
- Ledger lưu từng tranh luận: các vòng, bằng chứng, kết quả, bất đồng. Handoff và verdict có một dòng tóm
  tắt, ví dụ `Tranh luận: 4 · Lead nhận sai 2 · Lead giữ 1 · Lead quyết sau 2 vòng 1`.
- `status` có thống kê theo Lead và squad: Peer đồng ý hay phản biện; Lead nhận sai, giữ, quyết, leo thang,
  tự sửa brief;
  `blocked` theo kết quả `triage`; số lần approve dù CI fail. Supervisor đọc để thấy tranh luận
  có đang thành hình thức không.
- Giai đoạn sau, Patrol tự báo dấu hiệu xấu: Lead giữ ý từ 80% trở lên của ít nhất 5 tranh luận, Peer đồng
  ý gần như mọi brief, Lead trả lời quá hạn, commit chạm path đang khóa.

## 6. Tin nhắn

- Người nhận đang giữa lượt thì tin vào mailbox, không chen ngang. Người nhận đang rảnh thì tin được giao
  ngay và đánh thức họ.
- Khi có `agent.turn_ended`, desk gộp mọi tin đang chờ thành một message và gửi bằng `agents.ref(id).send`.
- Mỗi lần HC gọi tool squad, tin đang chờ được nối sau kết quả, mở đầu bằng `Message đang chờ:`.
- Gộp và đánh số, ví dụ `3 message: 1 done:T4 · 2 answer:A2 · 3 debate:T5`, rồi `--- 1/3 ---` và nội dung. Mỗi
  message kết bằng dòng `Next:` liệt kê bước được phép.
- Event của Paseo không phát lại, nên desk quét mailbox mỗi 30 giây. Tin cho HC đã archive bị bỏ.
- Human gõ thẳng vào chat của Lead hay Peer thì HC vẫn nhận tin, nhưng tin đó lách luật quyết định của Human
  phải thành ràng buộc có id. Desk ghi lại mọi tin squad gửi mỗi HC; hết lượt, tin người dùng nào trong
  timeline mà squad không gửi là tin của Human. Supervisor nhận nguyên văn để ghi bằng `amend_squad` nếu đó là
  quyết định, Lead nhận bản báo nếu tin gửi Peer, và HC được nhắc rằng việc vẫn theo brief. Mỗi tin chỉ báo
  một lần, và lượt đó không tính là stalled.
- Giai đoạn sau: lệnh dừng hoặc đổi hướng của cấp trên trực tiếp được cắt ngang lượt bằng
  `activeTurnBehavior: "steer"`. SDK 0.10.3 chưa có trường này trong type của `send`.

## 7. Tool squad

Agent thấy tool dưới dạng `mcp__squad__<tool>`. Server chỉ nhận tool thuộc tool set của người gọi.

| Vai trò | Core | Giai đoạn sau |
| --- | --- | --- |
| Supervisor | `open_squad`, `amend_squad`, `close_squad`, `message`, `answer`, `status` | `hold_squad`, `resume_squad`, `land`, `ask_human`, `rule`, `incidents` |
| Lead | `plan`, `respond`, `triage`, `amend_task`, `verdict`, `diff`, `message`, `ask`, `answer`, `report` | `cut_task` |
| Peer | `ask`, `propose`, `position`, `rebut`, `withdraw`, `handoff`, `blocked` | `need` |

- `message` đi theo cạnh của cây: Supervisor với Lead, Lead với Peer của mình. Tin Supervisor gửi Peer tự
  kèm cho Lead của Peer đó; Supervisor chỉ được hỏi, không giao việc.
- `diff{task, path?}` trả `git diff --stat` và diff của task từ `base` tới SHA đã gộp, có giới hạn kích thước.
- `status` trả squad, task, tranh luận và câu hỏi đang mở, tin đang chờ, thống kê tranh luận.
- `report` của Lead chạy lại `checks` của mọi task đã merge trên đầu nhánh squad, chuyển squad sang
  `ready` và gửi Supervisor kết quả. Nếu base đã đi tiếp từ lúc mở squad, plugin gộp thử base vào một
  worktree tạm rồi mới chạy CI, nên kết quả là thứ Human sẽ có sau khi merge. Gộp thử bị conflict thì message báo các
  file conflict và không chạy CI. Nhánh squad không bị đổi.
- `amend_squad{squad, note, constraints?, acceptance?}` ghi quyết định giữa chừng của Human vào squad đang
  `open`. Ràng buộc mới nhận id `c<n>` để Lead dùng làm nguồn trong `required`. Mọi thay đổi file, kể cả file
  luật hay tài liệu, vẫn là task do Peer làm.

## 8. Git

| Bước | Cách làm |
| --- | --- |
| Mở squad | `git branch squad/s1-pricing <base>` |
| Mở task | `git worktree add -b task/s1-t2-format <wt> squad/s1-pricing`. Nhánh task nằm dưới `task/` như seatworks, không lồng dưới nhánh squad vì git không cho `squad/s1-pricing` và `squad/s1-pricing/...` cùng tồn tại |
| Commit | Peer, chỉ trong worktree của task mình, với danh tính git `squad s1-t2` của HC để phân biệt với commit của Human |
| Handoff | Plugin `git merge squad/s1-pricing` trong worktree task nếu cần, rồi chạy lại `checks` như mục 4.2 |
| Approve | `git merge-base --is-ancestor squad/s1-pricing <sha>`, rồi `git update-ref` nhánh squad lên `sha` |
| Report | Base đi tiếp thì `git merge <base>` thử trong worktree tạm, chạy CI trên bản gộp, rồi bỏ worktree đó |
| Đóng squad | Archive các HC, `worktree remove`, xóa nhánh task đã merge, giữ nhánh squad cho Human |

- Plugin chạy git với `-c core.hooksPath=` rỗng và `-c core.fsmonitor=false`, để HC không cài được hook mà
  plugin chạy hộ.
- Shim `shim/git` bằng Node, có bản `.cmd` cho Windows, chặn `push`, `pull`, `fetch`, `checkout`, `switch`,
  `merge`, `rebase`, `reset --hard`, `stash`, `worktree`, `update-ref`, `branch -f`, `branch -D`, kể cả qua
  alias, và chặn lệnh ghi của Peer ngoài worktree của mình. `gh`, `paseo` được thay bằng script báo lỗi.
- Shim chỉ chống nhầm, vì gọi git bằng đường dẫn đầy đủ là vượt được. Hàng rào cứng là tool được phép, mẫu
  `deny` và việc server kiểm SHA.

## 9. Các lớp giữ vai trò

| Lớp | Cơ chế | Độ cứng |
| --- | --- | --- |
| 1 | Tool squad theo vai trò và peer, danh tính bằng secret | Cứng |
| 2 | `paseoTools.enabled: false` ở provider | Cứng |
| 3 | Danh sách cho phép `--tools` theo file định nghĩa, mẫu `deny`, `strict-mcp-config`, mọi permission bị từ chối, như mục 3.2 | Cứng với Claude |
| 4 | Hook `agent.create` từ chối HC squad không do desk đăng ký | Cứng |
| 5 | Worktree riêng mỗi task và shim git | Chống nhầm |
| 6 | Server kiểm schema, SHA, nguồn của ràng buộc, luật tranh luận | Cứng với mọi việc qua tool |
| 7 | Prompt theo vai trò và peer. Peer không thấy các từ `supervisor`, `paseo` | Mềm |

## 10. Luồng chính

```text
Khởi động   plugin mở MCP HTTP trên cổng cố định
            hook hoặc event đầu tiên mang API → ghi đè 3 provider → nhận lại agent theo label → giao message tồn
            Human tạo agent Supervisor (tiêu đề `supervisor`) → hook gắn HC, secret, prompt, tool → nói mục tiêu

Mở squad    Supervisor ─open_squad{goal, acceptance, slug, constraints[{text, source}], model?}─▶ desk
            desk tạo nhánh squad → HC chờ Lead → agents.create squad-claude-lead → message giao việc

Giao việc   Lead ─plan{briefs}─▶ desk kiểm schema và nguồn
            mỗi brief: nhánh và worktree task → workspace Paseo → HC chờ → agents.create squad-claude-peer
            với file peer brief chọn → vòng tranh luận ở mục 5
            task phụ thuộc: Lead chờ task trước merged rồi mới plan

Làm, chấm   Peer sửa, commit → handoff → gộp nhánh squad vào task → CI chạy lại checks → message ci_done tới Lead
            conflict → Peer gỡ, handoff lại · blocked → triage · ask → answer · stalled → nhắc, rồi báo Lead
            approve → fast-forward nhánh squad → archive Peer, dọn worktree
            nhánh squad đã đi tiếp → task về ci_running · request_changes → task về running, cùng HC

Kết thúc    Lead ─report─▶ chạy lại mọi checks trên nhánh squad → squad ready → Supervisor báo Human
            Human merge squad/s1-pricing lên main
            Supervisor ─close_squad─▶ archive HC còn lại, dọn worktree, squad closed, giữ ledger

Restart     đọc ledger, ngừng nếu hỏng → MCP trên cùng cổng → chờ API như lúc khởi động
            → nối lại HC theo label → làm lại việc dở: chạy lại CI cho task đang ci_running, fast-forward task đã approved,
              chạy CI cho squad đang report, tạo lại HC chưa có agent bằng message giao việc đã lưu
            HC mất agent thì báo Supervisor
```

## 11. Giao diện, giai đoạn sau

- Panel đội, `addWorkspacePanel`: squad, HC, task với trạng thái, `scope`, SHA, tranh luận từng vòng, bất
  đồng; tab "Chờ bạn" cho câu hỏi cần Human; tab thiết lập base branch, luật dự án, cổng, mã `S#`.
- Thẻ hỏi Human trong chat Supervisor, bằng `timeline.append` và `addTimelineRenderer`; trả lời xong thì
  thu lại một dòng.
- Pill "Chờ bạn quyết (n)" trên chat mọi HC, bằng `addComposerPill`.
- Panel và RPC cũng mang Paseo API tới plugin, nên lúc đó không còn phải chờ hoạt động agent.

## 12. Bố cục file

```text
squad/
  paseo-plugin.json        id "squad", requirements.paseo ">=0.10.3"
  index.server.ts          nối Host, Desk, Hooks, MCP HTTP; trả cleanup
  index.client.tsx         giai đoạn sau: panel, thẻ, pill
  shared/protocol.ts       Brief, Propose, Position, Challenge, Handoff, Verdict, Ask
  server/
    desk/                  squad, task, tranh luận, verdict; hàm thuần
    desk/names.ts          mọi luật đặt tên: mã, tiêu đề, nhánh, thư mục, mã tắt, danh tính commit
    ledger/                store, transact, migrate, đọc fail-closed
    outbox/mailbox.ts      message chờ giao và đã gửi, gộp message, nhận ra tin Human gõ thẳng vào HC
    git/                   worktree, gộp nhánh, fast-forward, chạy git an toàn
    hc/                    ghi đè provider, HC chờ, nạp vai trò, secret, cấu hình agent
    mcp/                   MCP HTTP, phân tool
    host/                  PaseoHost: giữ API từ hook, bọc SDK của Paseo
    harness/claude/        harness.json, settings.json chung, settings/<loại HC>.settings.json
    hc/content.ts          roles.json, content/ và server/harness/ nhúng vào code, sinh bằng scripts/embed-content.mjs
  roles.json               danh sách loại HC: supervisor, lead, peer
  content/prompts/         supervisor.md  lead.md  peer.md
  content/skills/          giai đoạn sau: skill theo vai trò
  shim/                    git, gh, paseo
  test/
```

Plugin chỉ tác động lên máy ở ba chỗ: nhánh và worktree trong repo của Human, 3 provider trong config Paseo,
dữ liệu dưới `<state>`. Không sửa file nào trong checkout của Human, không gọi mạng ra ngoài. Paseo không báo
cho plugin khi bị gỡ, nên sau `paseo plugin remove squad` Human đóng Paseo rồi chạy `node scripts/uninstall.mjs`
để xóa 3 provider. Provider còn sót cũng vô hại vì launcher từ chối chạy. Ledger được giữ.

## 13. Kiểm chứng

| Lớp | Cách làm |
| --- | --- |
| Unit | Desk, ledger, mailbox, schema là hàm thuần, test không cần Paseo |
| Host giả | `FakePaseoHost` mô phỏng tạo agent, gửi tin, event lượt, API tới muộn |
| MCP HTTP | Secret sai hoặc đã thu hồi nhận 401; tool ngoài tool set bị từ chối; gọi trước khi có API nhận "đang khởi động"; cổng bị chiếm thì báo lỗi rõ |
| Vai trò và loại HC | Peer gọi `verdict` hay tool không có bị từ chối; settings có tool cấm hay tên tool gõ sai, hoặc `roles.json` có vai trò thứ tư hay model ngoài provider, bị loại lúc khởi động; Lead không có `Bash`; reviewer không có `Edit`; Peer gọi `git push` bị từ chối kể cả khi settings của Human allow; Peer đọc được worktree của mình nhưng không đọc được ledger |
| Git thật | Worktree, gộp nhánh có và không có conflict, fast-forward, shim, trên repo tạm. Report khi base đã đi tiếp: chạy CI trên bản gộp thử, conflict thì báo file; commit của Peer mang danh tính của HC |
| Tích hợp nhánh | Hai task sửa khác file nhưng làm hỏng nhau: task nộp sau fail `checks` sau khi gộp. Task A merge trong lúc task B đã `ci_done`: B tự về `ci_running` |
| Kiến trúc | Cấm import chéo client và server, cấm gọi tên harness trong lõi |
| Giao thức | Phải bị từ chối: `required` không nguồn; verdict sai SHA; approve trước khi CI xong; approve khi CI fail mà thiếu `overFailed`; handoff khi còn thay đổi chưa commit; handoff trước `propose`; `triage keep` thiếu phản chứng |
| CI | Peer khai exit 0 nhưng lệnh thật fail thì message gửi Lead chỉ ra chỗ lệch. HEAD đổi sau handoff thì kết quả CI bị hủy |
| Tranh luận | Phải bị từ chối: `agree` thiếu `comparison`; `propose` lần hai; `hold` chỉ trỏ lại brief; `hold` hay `rebut` lặp bằng chứng, kể cả khi chỉ sửa câu trích hay output; câu trích không có trong file; `concede` không đổi gì hoặc đổi sai chỗ; `amend_task` khi còn tranh luận mở; `hold` lần ba; `decide` trước `deadlocked`; handoff khi còn tranh luận mở; `concede` đổi mục tiêu squad mà không `escalate` |
| Stalled | Peer kết thúc lượt không gọi tool: lần một được nhắc, lần hai Lead nhận báo. Peer chờ tranh luận thì không bị nhắc |
| Tin của Human | Human gõ thẳng vào chat của Peer: Supervisor và Lead được báo đúng một lần; message và message giao việc của squad không bị coi là tin của Human |
| Chất lượng tranh luận | Bộ brief cố ý cài sai, có bằng chứng trong repo; đo Peer có phản biện và Lead có nhận sai không. Chạy tay ở C3 |
| Khôi phục | `agents.create` timeout nhưng Paseo vẫn tạo: không sinh agent thứ hai. Kill giữa lúc gộp nhánh, ledger hỏng, reload giữa lượt |
| Lint | `lint-plugin.mjs` của skill paseo-plugin-design và typecheck |

## 14. Spike

Chạy ngày 2026-10-03 trên Paseo 0.10.3, Windows, bằng một plugin thử. Log và code nằm ngoài repo.

| # | Câu hỏi | Kết quả |
| - | --- | --- |
| S1 | API, label, `idempotencyKey`, `agents.create`, event lượt, `agent.created`, `options`, MCP HTTP, từ chối permission | Đạt. Lúc khởi động context không có API; API tới qua `session_open` hoặc `turn_started` và dùng được sau hook. `config.patch` thêm provider và `removeProviders` xóa được. Label lưu đúng. Hook `agent.create` nhận đủ `providerOptions`. `extraArgs.tools` là danh sách cho phép thật. `settings.permissions.deny` chặn `git push`. `respondToPermission` deny chạy và agent nhận lời nhắn. Agent gọi được tool qua MCP HTTP. `idempotencyKey` trùng với request khác trả `agent_request_key_conflict` |
| S2 | Workspace Paseo cho worktree do plugin tạo | Đạt. `workspaces.open(path)` trả workspace mới, agent tạo trong đó chạy đúng thư mục |
| S3 | Không còn tool `mcp__paseo__*` | Đạt. Với `paseoTools.enabled: false` và `strict-mcp-config`, HC không thấy tool Paseo hay server MCP nào khác |

Còn lại sau spike: việc lưu `options` qua restart daemon mới kiểm bằng đọc code; kiểm thật ở C4.

## 15. Lộ trình

| Mốc | Nội dung | Xong khi |
| --- | --- | --- |
| C0 | Spike S1 đến S3, khung plugin chỉ có phía server ở gốc repo, lint và typecheck sạch | Đã xong ngày 2026-10-03 |
| C1 | Ledger, protocol, desk thuần, unit test | Đã xong ngày 2026-10-03: 63 test qua, chưa cần Paseo |
| C2 | Host giữ API, ghi đè provider, hook, MCP HTTP, secret, outbox | Đã xong ngày 2026-10-04: chạy thật trên Paseo, Supervisor gọi `status` và `open_squad`, Lead được tạo và gọi `plan`, message được giao khi agent rảnh |
| C3 | `plan`, Peer trong worktree, đề xuất mù, tranh luận, handoff, gộp nhánh, chạy lại `checks`, verdict, fast-forward, git shim | Đã xong ngày 2026-10-04. Chạy thật với Lead và Peer sonnet: Lead cài sai `c1` dùng `toFixed(2)`, Peer phản biện bằng `RULES.md` dòng 3–4, Lead nhận sai, brief lên v2, task vào nhánh squad với `Math.floor`. Handoff khai sai output và hai task làm hỏng nhau được test trên git thật |
| C4 | Reviewer, `blocked` và `triage`, `escalate`, `amend_squad`, stalled, thống kê, `report` chạy lại CI, khôi phục sau restart | Đã xong ngày 2026-10-04. Chạy thật: reload plugin lúc Peer đang làm, task vẫn đi tới handoff, review và merge; reviewer chạy test và nêu finding P3; Human yêu cầu sửa `RULES.md` thì Supervisor ghi ràng buộc `C1`, Lead giao task `scope: [RULES.md]`, Peer sửa và merge. Bế tắc dẫn tới Lead quyết, `blocked` về vòng đề xuất, `escalate` và reject sau review được test bằng code |

| C5 | Chạy thật toàn bộ core sau khi tổ chức lại vai trò, launcher, giới hạn, luật tranh luận mới, `amend_task`, tin của Human, kiểm lệch base, danh tính commit, chặn settings cá nhân | Đã xong ngày 2026-10-04 trên repo nháp, Lead opus, Peer sonnet. Hai squad nối tiếp: hai Peer song song, review trước accept, `report` khi `main` đã đi tiếp thì kiểm trên bản gộp thử; Human merge rồi Supervisor đóng squad, worktree và nhánh task được dọn. Commit của Peer mang tên `squad S1.T1` (nay là `squad s1-t1`). Tin Human gõ thẳng vào chat Lead và Peer được báo Supervisor; Supervisor không ghi thành ràng buộc khi chưa hỏi lại Human. Claude giữ cờ `--setting-sources` đứng sau, nên HC không nạp settings cá nhân. Hai Peer đều đồng ý nên lần này không có tranh luận; luật tranh luận được test bằng code. Lượt chạy tìm ra và sửa hai lỗi: câu hỏi của Lead đã mất còn treo, và workspace của Lead bị archive tay không được dọn khi đóng squad. Lượt thứ hai, một mục tiêu bốn module: Lead giao bốn task song song với `scope` tách rời và để `src/index.js` chung cho task thứ năm sau khi bốn task merge. Hai Peer phản biện `scope` vì luật dự án bắt sửa `index.js`; Lead giữ ý kèm phản chứng mới, câu trích qua kiểm, Peer rút. Năm task merge, task đã `ci_done` tự kiểm lại khi task khác merge, kiểm trên nhánh squad đạt. Lượt thứ ba, một dự án ba squad song song (pricing, inventory, orders), 11 module, 14 task, giới hạn 8 Peer: hai squad đầu dùng hết 8 chỗ, Lead orders bị từ chối và đứng chờ vì không có gì đánh thức; đã sửa bằng message `hc_free` và không tính task của squad đã đóng. Human merge từng squad; squad cuối `report` thấy `main` đi tiếp 17 commit và báo conflict ở `src/index.js` trước khi Human merge. Sau khi gỡ conflict, 14 file test và 20 hàm export trên `main` đạt |

Giới hạn đã biết của core:

- Tin Human gõ vào HC đang chạy chỉ được nhận ra khi lượt đó kết thúc, nên HC có thể đã làm theo trước khi
  Supervisor biết. Prompt của Peer dặn hỏi Lead trước.
- Tin gõ vào HC ngay lúc squad archive nó thì mất cùng HC.
- Lead mất giữa chừng thì squad dừng; chưa có cách thay Lead.
- Chưa thử khởi động lại toàn bộ Paseo, chỉ mới reload plugin.
- Luật `scope` chỉ chặn chồng nhau trong một squad. Hai squad cùng sửa một file, như `src/index.js`, sẽ conflict
  khi Human merge squad sau; `report` báo trước conflict đó khi base đã đi tiếp, và Supervisor nhắc Human.
- Bằng chứng `doc` chỉ được ghi nhận như bên nộp khai. Lead từng trích chính prompt của mình làm tài liệu.
  Nên đối chiếu khi `ref` trỏ tới mục tiêu squad, ràng buộc hay luật dự án, vì desk có sẵn văn bản đó.

Giai đoạn sau:

- P1: panel đội, thẻ hỏi Human, pill.
- P2: mã `S#`, `ask_human` có phân loại, gate tự động, merge queue, land lên main, push.
- P3: Patrol, thời hạn trả lời, lệnh dừng khẩn, thay Lead đã mất.
- P4: biến thể của Peer `reviewer` (review độc lập ở đúng SHA, đã có trong core rồi bỏ ra ngày 2026-10-04 cho gọn), `scout`, `architect`, `need`, `hold_squad`, `cut_task`.
- Xa hơn: harness Codex, skill theo vai trò, màn Settings, ngân sách, mở PR.

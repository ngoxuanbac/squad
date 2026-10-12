# Squad

Đội agent Claude Code chạy native trên **Windows và Linux** (macOS chưa hỗ trợ), xem và theo dõi ngay trong **Paseo**.
Một binary Go + một file SQLite, không gì chạy trên cloud.

- **Mailbox chung**: agent nhắn nhau bằng tool `send`/`inbox`. Mail chỉ được ack khi lượt (turn) xử lý nó hoàn tất; worker crash, bị kill hay máy reboot thì mail được gửi lại.
- **Gate**: mỗi mail và mỗi lần spawn đều được daemon kiểm tra theo template (YAML) của team, không phụ thuộc prompt.
- **Taskforce**: team tạm (`dual-lens`, `advisor`) trả lời người gọi bằng mail. Role nào có `can_call_taskforce` cũng gọi được (ví dụ `lead` trong `slp`), không chỉ gate.
- **Paseo**: plugin chỉ đọc hiển thị team, thành viên, sự kiện và log của từng worker.

## Cài đặt

```sh
go build -o squad.exe ./cmd/squad     # Linux: -o squad; đặt vào PATH
squad setup claude                    # thêm MCP server "squad" (user scope) vào Claude Code
squad setup paseo                     # cài plugin vào Paseo (>= 0.11.2), rồi reload app
squad setup                           # xem trạng thái
```

## Bắt đầu nhanh

1. Mở Claude Code (hoặc agent trong Paseo) trong project.
2. Bảo nó: *"found a lead-peer team để sửa test đang fail"*.
3. Theo dõi: `squad top`, `squad tail <worker> -f`, hoặc màn hình **Squad** trong Paseo.

Thêm:

- [docs/guide.md](docs/guide.md): chạy team, gọi taskforce, theo dõi và can thiệp, khi có gì dừng giữa chừng.
- [docs/reference.md](docs/reference.md): mọi lệnh, file, biến môi trường, khóa template, tool cho agent và định dạng `--view`.

## Template có sẵn

| Template | Ai làm gì |
|---|---|
| `p2p` | Peer ngang hàng, tự spawn thêm peer |
| `lead-peer` | Lead giữ kế hoạch và chấm kết quả; peer giữ từng phạm vi |
| `slp` | Supervisor → lead mỗi lane → peer; lead gọi được `dual-lens`/`advisor` |
| `dual-lens` | Taskforce: chair hỏi 2 lens trên 2 model khác nhau, bắt phản biện, kết luận |
| `advisor` | Taskforce một agent: lời khuyên read-only kèm phép kiểm chứng |

Tạo template riêng: `squad template new mine --from slp`.

## Vận hành

- `~/.squad/config.yaml`: harness mặc định, gc, `spawn.allowed_roots`, shared prompts. Sửa xong chạy `squad check` rồi `squad restart`.
- `squad gc [--dry-run]`: archive rồi xoá team đã đóng / session solo đã đi quá `gc.closed_after` (mặc định 14 ngày; daemon tự chạy mỗi ngày). Xem lại: `squad archive list|show`.
- `squad model <worker> [model] [--thinking L]`: đổi model/effort của worker, có hiệu lực từ lượt sau.
- `squad setup remove claude|paseo`: gỡ squad.

## Giới hạn hiện tại

- Worker chạy `--permission-mode bypassPermissions`.
- Admin token nằm trong `~/.squad/daemon.json`: chỉ chống nhầm lẫn trong máy một người dùng, chưa phải ranh giới bảo mật.

## Build từ source

```sh
go build ./cmd/squad
go test ./...          # gồm e2e/: daemon, CLI và agent thật (fake claude), không cần mạng
go test -short ./...   # bỏ qua e2e
```

Trong `e2e/`, mỗi agent là fake claude (`internal/core/testdata/fakeclaude`): nó làm theo các dòng `@send`, `@agent`, `@sleep`, `@exit`, `@once`, `@await`, `@permission`, `@esc` trong mail nhận được, gọi `squad mcp` thật và kiểm tham số theo schema của tool như Claude. Với `FAKE_HOOKS=1` nó là session có plugin hooks: chạy `squad hook claude <Event>` và nghe messaging socket để được đánh thức.

- `e2e/testdata/*.view.json`: `ps --view` và `tail --view` thật, là hợp đồng với plugin Paseo (`extensions/paseo/test/contract.test.ts` đọc lại từng trường; `go test` chạy nó khi có `node`). Đổi có chủ ý: `go test ./e2e -run TestViewContract -update`.
- Chạy e2e ở máy không có Go (ví dụ Linux qua WSL): build sẵn `squad`, `claude` (fake) và `go test -c ./e2e` với `GOOS=linux`, rồi chạy `e2e.test` trong `e2e/` với `SQUAD_E2E_BIN=<thư mục chứa squad và claude>`.
- Coverage của binary qua e2e: đặt `GOCOVERDIR=<dir>` rồi `go tool covdata percent -i=<dir>`.
- Với Claude thật (tốn tiền, cần mạng và `claude` đã đăng nhập): `SQUAD_LIVE=1 go test ./e2e -run TestLive` chạy worker trên haiku (mỗi worker `--max-budget-usd 1`), `SQUAD_LIVE=all` thêm review `dual-lens` (lens trên sonnet và opus). Mỗi test in số tiền worker đã tiêu; cả bộ khoảng $0.4.

## License

MIT

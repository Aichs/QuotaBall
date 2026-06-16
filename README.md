# QuotaBall

QuotaBall 是一个 Go/Wails 桌面版额度监控工具，用于在 Windows 桌面上查看 Krill AI 与 NewAPI 账号的额度、余额和使用情况。应用提供主面板、系统托盘和玻璃球浮窗，适合长期挂在桌面后台查看状态。

## Features

- **双登录模式**：支持 Krill AI 账号登录，也支持 NewAPI 通过 LinuxDo 授权登录。
- **Krill AI 面板**：展示本周剩余、钱包余额、周额度、月总额度，以及每张套餐卡的周/月进度。
- **NewAPI 面板**：展示当前余额、历史消耗、请求次数；NewAPI 没有固定总额度，玻璃球始终显示满水位。
- **玻璃球浮窗**：Krill AI 模式下可点击切换周额度 / 月总额度；NewAPI 模式下显示余额数值。
- **系统托盘**：支持后台运行、显示/隐藏主面板、手动刷新、退出程序。
- **设置面板**：可配置刷新间隔、窗口置顶、记住登录状态、玻璃球显示和 Codex Fast 代理。
- **Codex Fast 代理**：可一键为 Codex Desktop 切换本地代理，自动注入第三方 API 需要的 Fast 请求参数。
- **持久化保护**：配置文件使用原子写入，敏感登录信息通过本地 secret store 保存。

## Screens

- 主面板：集中展示当前账号的额度摘要和套餐详情。
- 登录页：可切换 NewAPI、Sub2、Krill AI 登录方式；Sub2 入口保留但暂未开放。
- 关于页：展示软件信息、作者、项目链接和 LinuxDo 社区入口。

## Build

环境要求：

- Windows
- Go 1.25+

一键构建：

```bat
build_go.bat
```

脚本会先执行测试，再生成：

- `dist\QuotaBall.exe`
- `%USERPROFILE%\Desktop\QuotaBall.exe`

手动构建：

```powershell
go test ./...
go build -tags production -trimpath -ldflags "-H=windowsgui -s -w" -o .\dist\QuotaBall.exe .\cmd\quotaball
```

开发运行：

```powershell
go run .\cmd\quotaball
```

## Configuration

首次运行会自动创建配置文件。常用字段：

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `provider` | 当前登录方式：`krill` / `newapi` | `krill` |
| `email` | Krill AI 登录邮箱 | 空 |
| `newapi_base_url` | NewAPI 站点地址 | 空 |
| `remember_login` | 是否记住登录状态 | `true` |
| `refresh_sec` | 自动刷新间隔，最小 3 秒 | `60` |
| `opacity` | 窗口透明度 | `0.96` |
| `on_top` | 主窗口置顶 | `true` |
| `theme` | 主题：`light` / `dark` | `light` |
| `tbar_enabled` | Krill AI 模式下是否显示玻璃球 | `true` |
| `tbar_metric` | Krill AI 玻璃球指标：`weekly` / `monthly` | `weekly` |
| `codex_fast_proxy_enabled` | 是否启用 Codex Fast 本地代理开关 | `false` |

密码和 OAuth 会话不会以明文写入 `config.json`；保存配置时会清空明文密码字段。

Codex Fast 代理开关会更新当前用户的 `~/.codex/config.toml`，把 Codex 的第三方 API 地址切到本地代理或恢复为直连 Krill AI API。启用时会部署并启动 `~/.codex/fast-proxy` 下的 Node 代理，同时注册当前用户的开机启动项；关闭时会停止代理并移除该启动项。切换后需要重启 Codex Desktop 或新开 Codex 会话，确保 Codex 重新读取配置。

## Usage

- **打开设置**：点击主面板右上角齿轮按钮。
- **查看关于页**：点击齿轮旁边的 `i` 按钮。
- **手动刷新**：点击主面板右上角刷新按钮，或使用托盘菜单。
- **显示/隐藏主面板**：双击托盘图标，或右键托盘选择显示 / 隐藏。
- **退出程序**：右键托盘图标后选择退出。
- **切换 Krill AI 玻璃球指标**：点击玻璃球中心，或使用玻璃球右键菜单。
- **切换 Codex Fast 代理**：打开设置，勾选或取消 `Codex Fast 代理` 后保存，再重启 Codex Desktop 或新开 Codex 会话。

## Project Layout

```text
cmd\quotaball\                 # Windows GUI 程序入口
internal\config\               # 配置加载、迁移、保存
internal\krill\                # Krill AI API 与额度模型
internal\newapi\               # NewAPI OAuth、用户信息与用量模型
internal\secret\               # 本地 secret store
internal\ui\                   # Windows 托盘、玻璃球和原生 UI
internal\wailsui\              # Wails 主面板、前端资源和绑定
internal\codexfast\            # Codex Fast 本地代理部署、配置和进程管理
```

## Testing

常规测试：

```powershell
go test ./...
```

覆盖率：

```powershell
go test -cover ./...
```

空白检查：

```powershell
git diff --check
```


## 🤝 Friends / Links

<table border="0">
  <tbody>
    <tr>
      <td width="200" align="center">
        <a href="https://linux.do" target="_blank" style="text-decoration:none;">
          <img src="https://img.shields.io/badge/LINUX.DO-Community-000000?style=for-the-badge&logo=linux&logoColor=white" alt="LINUX.DO" />
        </a>
      </td>
      <td align="left">
        <strong><a href="https://linux.do" target="_blank">LINUX.DO</a></strong><br/>
        真诚、友善、团结、专业，共建你我引以为荣之社区。
      </td>
    </tr>
  </tbody>
</table>

$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$NodeCandidates = @()
if ($env:CODEX_FAST_PROXY_NODE) {
  $NodeCandidates += $env:CODEX_FAST_PROXY_NODE
}

$RuntimeRoot = Join-Path $env:LOCALAPPDATA "OpenAI\Codex\runtimes\cua_node"
if (Test-Path $RuntimeRoot) {
  $NodeCandidates += Get-ChildItem -Path $RuntimeRoot -Recurse -Filter node.exe -ErrorAction SilentlyContinue |
    Sort-Object LastWriteTime -Descending |
    Select-Object -ExpandProperty FullName
}

$NodeCandidates += @(
  "C:\Program Files\nodejs\node.exe",
  "C:\Program Files (x86)\nodejs\node.exe",
  "C:\nvm4w\nodejs\node.exe",
  "node.exe"
)

$Node = $null
foreach ($Candidate in $NodeCandidates) {
  try {
    if (Test-Path $Candidate) {
      $Node = (Resolve-Path $Candidate).Path
      break
    }
    $Command = Get-Command $Candidate -ErrorAction Stop
    if ($Command.Source) {
      $Node = $Command.Source
      break
    }
  } catch {
  }
}

if (-not $Node) {
  throw "Node.js was not found for Codex fast proxy."
}

$env:CODEX_FAST_PROXY_HOST = "{{HOST}}"
$env:CODEX_FAST_PROXY_PORT = "{{PORT}}"
$env:CODEX_FAST_PROXY_TARGET_ORIGIN = "{{TARGET_ORIGIN}}"
$env:CODEX_FAST_PROXY_MODELS = "{{MODELS}}"
$env:CODEX_FAST_PROXY_LOG = Join-Path $Root "fast-proxy.log"

Set-Location $Root
& $Node (Join-Path $Root "codex-fast-proxy.mjs")

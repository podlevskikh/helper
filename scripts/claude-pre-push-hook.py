#!/usr/bin/env python3
"""Хук Claude Code (PreToolUse, Bash): пуш в main бэкенда — это выкатка на прод, поэтому
перед ним обязательно scripts/release-check.sh. Если проверка не прошла — пуш блокируется (exit 2).

Срабатывает на `git push` в репозитории бэкенда (helper-backend*), когда цель — main:
`git push origin main`, `git push origin HEAD:main`, `git push` из ветки main.
Подключён в .claude/settings.json бэкенда и фронта. Аварийный обход (только с разрешения владелицы):
SKIP_RELEASE_CHECK=1 в той же команде.
"""
import json
import os
import re
import subprocess
import sys

try:
    data = json.load(sys.stdin)
except ValueError:
    sys.exit(0)
if data.get("tool_name") != "Bash":
    sys.exit(0)
cmd = (data.get("tool_input") or {}).get("command", "")
if not re.search(r"\bgit\b[^;&|]*\bpush\b", cmd) or "SKIP_RELEASE_CHECK=1" in cmd:
    sys.exit(0)

# Какой репозиторий пушат: путь из `cd …`/`-C …` с helper-backend, иначе текущая папка.
repo = None
for m in re.finditer(r"(?:cd|-C)\s+(\"[^\"]+\"|'[^']+'|\S+)", cmd):
    p = os.path.expanduser(m.group(1).strip("'\""))
    p = p if os.path.isabs(p) else os.path.join(data.get("cwd") or os.getcwd(), p)
    if os.path.isfile(os.path.join(p, "cmd", "server", "main.go")):
        repo = os.path.normpath(p)
if repo is None:
    cwd = data.get("cwd") or os.getcwd()
    if os.path.isfile(os.path.join(cwd, "cmd", "server", "main.go")):
        repo = cwd
if repo is None:
    sys.exit(0)  # пушат не бэкенд

push = re.search(r"\bpush\b([^;&|]*)", cmd).group(1)
to_main = bool(re.search(r"(^|\s|:)main(\s|$)", push))
if not to_main and not re.search(r"\S+\s+\S+", push.strip()):  # `git push` без явной цели — смотрим текущую ветку
    branch = subprocess.run(["git", "-C", repo, "rev-parse", "--abbrev-ref", "HEAD"], capture_output=True, text=True).stdout.strip()
    to_main = branch == "main"
if not to_main:
    sys.exit(0)

script = os.path.join(repo, "scripts", "release-check.sh")
if not os.path.isfile(script):
    sys.exit(0)
r = subprocess.run([script], cwd=repo, capture_output=True, text=True)
if r.returncode != 0:
    tail = "\n".join((r.stdout + r.stderr).strip().splitlines()[-25:])
    print(f"Пуш в main бэкенда = выкатка на прод. scripts/release-check.sh не прошёл — пуш остановлен.\n{tail}", file=sys.stderr)
    sys.exit(2)
sys.exit(0)

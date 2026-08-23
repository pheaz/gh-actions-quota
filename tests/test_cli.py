import json
import subprocess
from decimal import Decimal
from pathlib import Path

import pytest

import github_actions_quota.__main__ as command


def _configure(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> tuple[Path, Path]:
    output, summary = tmp_path / "output", tmp_path / "summary"
    monkeypatch.setenv("ACTIONS_QUOTA_TOKEN", "secret")
    monkeypatch.setenv("GITHUB_REPOSITORY_OWNER", "repository-owner")
    monkeypatch.setenv("GITHUB_OUTPUT", str(output))
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(summary))
    monkeypatch.delenv("GITHUB_ACTIONS", raising=False)
    return output, summary


def test_cli_writes_usage_and_uses_repository_owner_not_actor(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    output, summary = _configure(monkeypatch, tmp_path)
    monkeypatch.setenv("ACTIONS_QUOTA_MINUTES", "3000")
    monkeypatch.setenv("GITHUB_ACTOR", "unrelated-actor")
    seen: list[tuple[str, str, str | None]] = []

    def fake_owner_type(_owner: str, _token: str) -> str:
        return "user"

    def fake_used_minutes(
        owner: str,
        token: str,
        *,
        owner_type: str | None = None,
    ) -> Decimal:
        seen.append((owner, token, owner_type))
        return Decimal("1200")

    monkeypatch.setattr(command, "fetch_owner_type", fake_owner_type)
    monkeypatch.setattr(command, "fetch_used_minutes", fake_used_minutes)
    assert command.main() == 0
    assert output.read_text().splitlines() == [
        "usage_available=true",
        "used_minutes=1200",
        "quota_minutes=3000",
        "usage_percent=40.0",
        "billing_owner=repository-owner",
        "billing_owner_type=user",
    ]
    assert seen == [("repository-owner", "secret", "user")]
    assert "40.0%" in summary.read_text()


def test_default_quota_and_api_failure_fail_closed(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    output, _ = _configure(monkeypatch, tmp_path)

    def fake_owner_type(_owner: str, _token: str) -> str:
        return "user"

    def fake_used_minutes(
        _owner: str,
        _token: str,
        *,
        owner_type: str | None = None,
    ) -> Decimal:
        del owner_type
        raise OSError("down")

    monkeypatch.setattr(command, "fetch_owner_type", fake_owner_type)
    monkeypatch.setattr(command, "fetch_used_minutes", fake_used_minutes)
    assert command.main() == 0
    assert output.read_text().splitlines() == [
        "usage_available=false",
        "used_minutes=unavailable",
        "quota_minutes=2000",
        "usage_percent=unavailable",
    ]
    assert "::warning" in capsys.readouterr().out


def test_local_uses_gh_for_token_and_owner(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    output, _ = _configure(monkeypatch, tmp_path)
    monkeypatch.delenv("ACTIONS_QUOTA_TOKEN")
    monkeypatch.delenv("GITHUB_REPOSITORY_OWNER")
    calls: list[list[str]] = []

    def run(args: list[str], **_kwargs: object) -> subprocess.CompletedProcess[str]:
        calls.append(args)
        value = "local-owner\n" if args[1] == "repo" else "gh-token\n"
        return subprocess.CompletedProcess(args, 0, value, "")

    monkeypatch.setattr(command.subprocess, "run", run)

    def fake_owner_type(_owner: str, _token: str) -> str:
        return "organization"

    def fake_used_minutes(
        _owner: str,
        _token: str,
        *,
        owner_type: str | None = None,
    ) -> Decimal:
        del owner_type
        return Decimal(1)

    monkeypatch.setattr(command, "fetch_owner_type", fake_owner_type)
    monkeypatch.setattr(command, "fetch_used_minutes", fake_used_minutes)
    assert command.main() == 0
    assert calls == [
        ["gh", "repo", "view", "--json", "owner", "--jq", ".owner.login"],
        ["gh", "auth", "token"],
    ]
    assert "billing_owner=local-owner" in output.read_text()


def test_public_actions_repository_is_unmetered_without_token(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    output, summary = _configure(monkeypatch, tmp_path)
    monkeypatch.delenv("ACTIONS_QUOTA_TOKEN")
    monkeypatch.setenv("GITHUB_ACTIONS", "true")
    event = tmp_path / "event.json"
    event.write_text(
        json.dumps(
            {"repository": {"private": False, "owner": {"type": "Organization"}}}
        )
    )
    monkeypatch.setenv("GITHUB_EVENT_PATH", str(event))

    def fail_used_minutes(
        _owner: str,
        _token: str,
        *,
        owner_type: str | None = None,
    ) -> Decimal:
        del owner_type
        pytest.fail("API called")

    monkeypatch.setattr(command, "fetch_used_minutes", fail_used_minutes)
    assert command.main() == 0
    assert "usage_available=true" in output.read_text()
    assert "usage_percent=0" in output.read_text()
    assert "billing_owner_type=organization" in output.read_text()
    assert "Standard GitHub-hosted runners" in summary.read_text()


@pytest.mark.parametrize("quota", ["bad", "0", "-1", "NaN", "Infinity"])
def test_invalid_quota_fails_closed(
    quota: str, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    output, _ = _configure(monkeypatch, tmp_path)
    monkeypatch.setenv("ACTIONS_QUOTA_MINUTES", quota)
    assert command.main() == 0
    assert "usage_available=false" in output.read_text()

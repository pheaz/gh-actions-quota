from decimal import Decimal
from pathlib import Path

import pytest

import github_actions_quota.__main__ as command


def _configure(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> tuple[Path, Path]:
    output = tmp_path / "output"
    summary = tmp_path / "summary"
    monkeypatch.setenv("GITHUB_ACTIONS_QUOTA_TOKEN", "secret")
    monkeypatch.setenv("GITHUB_REPOSITORY_OWNER", "owner")
    monkeypatch.setenv("GITHUB_OUTPUT", str(output))
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(summary))
    return output, summary


def test_cli_writes_success_outputs(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    output, summary = _configure(monkeypatch, tmp_path)
    monkeypatch.setenv("GITHUB_ACTIONS_QUOTA_MINUTES", "3000")
    monkeypatch.setattr(
        command, "fetch_used_minutes", lambda _user, _token: Decimal("1200")
    )
    assert command.main() == 0
    assert output.read_text().splitlines() == [
        "ci_state=full",
        "ci_reason=within_quota",
        "full_ci=true",
        "minimal_ci=true",
        "used_minutes=1200",
        "quota_minutes=3000",
        "usage_percent=40.0",
    ]
    assert "State: `full`" in summary.read_text()


@pytest.mark.parametrize(
    ("used", "state", "reason", "notice"),
    [
        ("1000", "quota_limited", "actions_quota_threshold", "Full CI skipped"),
        ("2000", "quota_exhausted", "actions_quota_exhausted", "CI skipped"),
    ],
)
def test_cli_reports_reduced_quota_notice(
    used: str,
    state: str,
    reason: str,
    notice: str,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    output, _summary = _configure(monkeypatch, tmp_path)
    monkeypatch.setattr(command, "fetch_used_minutes", lambda _u, _t: Decimal(used))
    assert command.main() == 0
    text = output.read_text()
    assert f"ci_state={state}" in text
    assert f"ci_reason={reason}" in text
    diagnostic = capsys.readouterr().out
    assert "::notice title=CI quota policy::" in diagnostic
    assert notice in diagnostic


def test_api_failure_fails_closed_without_failure_exit(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    output, summary = _configure(monkeypatch, tmp_path)

    def fail(_username: str, _token: str) -> Decimal:
        raise OSError("API unavailable\nunsafe line")

    monkeypatch.setattr(command, "fetch_used_minutes", fail)
    assert command.main() == 0
    assert output.read_text().splitlines() == [
        "ci_state=unavailable",
        "ci_reason=billing_usage_unavailable",
        "full_ci=false",
        "minimal_ci=false",
        "used_minutes=unavailable",
        "quota_minutes=2000",
        "usage_percent=unavailable",
    ]
    assert "::warning" in capsys.readouterr().out
    assert "State: `unavailable`" in summary.read_text()


@pytest.mark.parametrize("quota", ["bad", "0", "-1", "NaN", "Infinity"])
def test_invalid_quota_fails_closed(
    quota: str, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    output, _summary = _configure(monkeypatch, tmp_path)
    monkeypatch.setenv("GITHUB_ACTIONS_QUOTA_MINUTES", quota)
    assert command.main() == 0
    assert "full_ci=false\nminimal_ci=false" in output.read_text()

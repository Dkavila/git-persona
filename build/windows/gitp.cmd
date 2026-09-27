@echo off
REM ---------------------------------------------------------------------------
REM  gitp.cmd - short alias wrapper for git-persona.
REM
REM  Windows has no dependable symlink story for ordinary users: symlinks need
REM  Developer Mode or elevation, hard links need the same volume, and neither
REM  survives a zip round trip. A batch wrapper is the portable equivalent.
REM
REM  %~dp0 expands to the directory this script lives in, with a trailing
REM  backslash, so the wrapper resolves git-persona.exe as its own neighbour
REM  instead of depending on PATH order. It is quoted because the default
REM  install directory contains a space ("Git Persona").
REM
REM  %* forwards every argument verbatim, preserving the caller's quoting.
REM ---------------------------------------------------------------------------

"%~dp0git-persona.exe" %*

REM Propagate the child's exit code so scripts and CI can branch on failure.
exit /b %ERRORLEVEL%

<#
.SYNOPSIS
  Sets the Lightspeak version in every file that carries it.

.EXAMPLE
  ./scripts/bump-version.ps1 1.2.3
#>
param([Parameter(Mandatory)][string]$Version)
$ErrorActionPreference = 'Stop'
if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "Version must look like 1.2.3, got '$Version'" }
$client = Join-Path (Split-Path -Parent $PSScriptRoot) 'client'
$utf8 = [Text.UTF8Encoding]::new($false)

# Replaces the version in the first $count matches of a pattern whose group 1
# is the text in front of the version.
function Set-Version([string]$file, [string]$pattern, [int]$count = 1) {
  $path = Join-Path $client $file
  $text = [IO.File]::ReadAllText($path, $utf8)
  $regex = [regex]::new($pattern)
  if ($regex.Matches($text).Count -lt $count) { throw "Version not found in $file" }
  $new = $regex.Replace($text, { param($m) $m.Groups[1].Value + $Version }, $count)
  [IO.File]::WriteAllText($path, $new, $utf8)
}

Set-Version 'package.json' '("version": ")\d+\.\d+\.\d+'
Set-Version 'package-lock.json' '("version": ")\d+\.\d+\.\d+' 2
Set-Version 'src-tauri/tauri.conf.json' '("version": ")\d+\.\d+\.\d+'
Set-Version 'src-tauri/Cargo.toml' '(?m)(^version = ")\d+\.\d+\.\d+'
Set-Version 'src-tauri/Cargo.lock' '(name = "lightspeak"\r?\nversion = ")\d+\.\d+\.\d+'
Write-Host "Version set to $Version. Commit it as: chore(release): $Version"

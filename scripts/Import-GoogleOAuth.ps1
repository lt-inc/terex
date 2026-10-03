<#
.SYNOPSIS
Imports a downloaded Google Web OAuth client into the local environment file.
.EXAMPLE
./scripts/Import-GoogleOAuth.ps1 -ClientJsonPath 'C:/Users/me/Downloads/client_secret.json'
.EXAMPLE
./scripts/Import-GoogleOAuth.ps1 -ClientJsonPath './client.json' -RedirectUri 'https://terex.legendary-trio.me/api/accounts/oauth/callback'
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$ClientJsonPath,

    [string]$EnvPath = (Join-Path $PSScriptRoot '../.env'),

    [string]$RedirectUri = 'http://localhost:3000/api/accounts/oauth/callback'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-RequiredClientString {
    param([object]$WebClient, [string]$PropertyName)
    $property = $WebClient.PSObject.Properties[$PropertyName]
    if ($null -eq $property -or $property.Value -isnot [string] -or [string]::IsNullOrWhiteSpace($property.Value)) {
        throw "Google Web client JSON is missing $PropertyName."
    }
    $value = [string]$property.Value
    if ($value.IndexOfAny([char[]]@([char]0, [char]10, [char]13)) -ge 0) {
        throw "Google Web client JSON contains an invalid $PropertyName."
    }
    return $value
}

function Set-EnvironmentValue {
    param([string[]]$Lines, [string]$Name, [string]$Value)
    # Compose treats single-quoted dotenv values literally, including dollar signs.
    $replacement = $Name + "='" + $Value.Replace("'", "\'") + "'"
    $pattern = '^\s*(?:export\s+)?' + [regex]::Escape($Name) + '\s*='
    $result = [System.Collections.Generic.List[string]]::new()
    $replaced = $false
    foreach ($line in $Lines) {
        if ($line -match $pattern) {
            if (-not $replaced) {
                $result.Add($replacement)
                $replaced = $true
            }
        } else {
            $result.Add($line)
        }
    }
    if (-not $replaced) { $result.Add($replacement) }
    return $result.ToArray()
}

$parsedUri = $null
if ($RedirectUri.IndexOfAny([char[]]@([char]0, [char]10, [char]13)) -ge 0 -or
    -not [Uri]::TryCreate($RedirectUri, [UriKind]::Absolute, [ref]$parsedUri) -or
    $parsedUri.Scheme -notin @('http', 'https') -or
    [string]::IsNullOrWhiteSpace($parsedUri.Host)) {
    throw 'RedirectUri must be an absolute HTTP or HTTPS callback URL.'
}

try {
    $inputFile = [IO.Path]::GetFullPath($ClientJsonPath)
    $document = [IO.File]::ReadAllText($inputFile) | ConvertFrom-Json
} catch {
    throw 'Unable to read the Google client JSON. Supply a local downloaded JSON file.'
}
$webProperty = $null
if ($null -ne $document) { $webProperty = $document.PSObject.Properties['web'] }
if ($null -eq $webProperty -or $null -eq $webProperty.Value) {
    throw 'A Google OAuth Web application client JSON (web object) is required.'
}
$webClient = $webProperty.Value
$clientID = Get-RequiredClientString $webClient 'client_id'
$clientSecret = Get-RequiredClientString $webClient 'client_secret'
$redirects = $webClient.PSObject.Properties['redirect_uris']
if ($null -eq $redirects -or @($redirects.Value) -cnotcontains $RedirectUri) {
    throw 'The requested callback is absent from JSON.web.redirect_uris. Use the JSON saved at client creation or verify the Google callback configuration before changing environment values manually.'
}

$environmentFile = [IO.Path]::GetFullPath($EnvPath)
$parentDirectory = [IO.Path]::GetDirectoryName($environmentFile)
if (-not [IO.Directory]::Exists($parentDirectory)) {
    throw 'The environment file parent directory must already exist.'
}
try {
    [string[]]$lines = @()
    if ([IO.File]::Exists($environmentFile)) {
        $lines = [IO.File]::ReadAllLines($environmentFile)
    }
    $lines = Set-EnvironmentValue $lines 'GOOGLE_CLIENT_ID' $clientID
    $lines = Set-EnvironmentValue $lines 'GOOGLE_CLIENT_SECRET' $clientSecret
    $lines = Set-EnvironmentValue $lines 'GOOGLE_REDIRECT_URL' $RedirectUri
} catch {
    throw 'Unable to read or update the environment file.'
}

# A same-directory temporary file keeps replacement atomic. The default name is
# covered by the repository .env.*.local ignore rule, including on interruption.
$temporaryFile = Join-Path $parentDirectory ('.env.oauth-import-' + [Guid]::NewGuid().ToString('N') + '.local')
try {
    $text = [string]::Join([Environment]::NewLine, $lines) + [Environment]::NewLine
    [IO.File]::WriteAllText($temporaryFile, $text, [Text.UTF8Encoding]::new($false))
    if ([IO.File]::Exists($environmentFile)) {
        [IO.File]::Replace($temporaryFile, $environmentFile, [System.Management.Automation.Language.NullString]::Value)
    } else {
        [IO.File]::Move($temporaryFile, $environmentFile)
    }
} catch {
    throw 'Unable to write the environment file. Existing values were not printed.'
} finally {
    if ([IO.File]::Exists($temporaryFile)) {
        Remove-Item -LiteralPath $temporaryFile -Force
    }
}

Write-Output "Google OAuth settings imported into $environmentFile. Credential values were not printed."

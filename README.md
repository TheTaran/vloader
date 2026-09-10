# vloader

Eine schlanke Go-/Docker-WebGUI für deine Emby-Sammlung. Dunkles Kino-Design, lokale und OIDC-Anmeldung, gespiegelte Bibliotheken mit Metadaten und Bildern sowie Downloads direkt in den Browser.

## Start

```sh
cp .env.example .env
# ADMIN_PASSWORD mit mindestens 16 Zeichen setzen, z. B. openssl rand -hex 24
# APP_URL auf den exakten Browser-Ursprung setzen, ohne abschließenden Slash.
docker compose up -d --build
```

Bei dieser Erstinstallation wurde `.env` bereits mit einem zufälligen Admin-Passwort angelegt (Dateirechte 0600). **Nicht überschreiben.** Das Passwort lokal aus `.env` entnehmen; Benutzername ist `admin`. Standardadresse: http://localhost:8090. Der Port ist nur auf Loopback veröffentlicht. Bei Zugriff über SSH: `ssh -L 8090:127.0.0.1:8090 devadmin@DEIN-HOST`.

Für LAN-Zugriff BIND_ADDRESS und APP_URL passend setzen; für entfernten Zugriff HTTPS über einen Reverse Proxy verwenden. APP_URL muss dem Browser-Ursprung entsprechen, sonst werden schreibende Aufrufe abgewiesen. Keine Änderung an caddymgm ist notwendig.

## Emby verbinden

Unter **Verbindungen** Server-URL (optional mit `/emby`-Basispfad) und API-Schlüssel speichern. Den Schlüssel in Emby unter **Erweitert → API-Schlüssel** erstellen. Anschließend in **Sammlung → Synchronisieren** den Katalog übernehmen.

Bibliotheks- und Titel-IDs, ursprüngliche Elternzuordnung, Beschreibung, Jahr, Genres, Bewertung, Laufzeit, Bildreferenzen und Medienquellen werden gespiegelt. Bilder lädt vloader authentisiert von Emby bei Bedarf; kein vollständiges Offline-Bildarchiv. Katalog und Einstellungen liegen persistent im Docker-Volume. Eine fehlgeschlagene Synchronisierung erhält den letzten vollständigen Katalog. Die Anwendung erstellt keine neuen Libraries auf einem zweiten Emby-Server.

Titel anklicken → Details → **Datei herunterladen**. Filme und Episoden sind einzeln herunterladbar; Serien-/Ordnerobjekte nicht. Downloads gehen in den Browser, nicht in eine serverseitige Warteschlange. Emby-Downloads erfordern einen erreichbaren Server und passende API-Berechtigung. Große Dateien werden gestreamt, nicht in den RAM geladen.

## NFS

NFS-Server und Export in `.env` setzen, beispielsweise `NFS_SERVER=192.0.2.10`, `NFS_EXPORT=/exports/movies`. Die Export-Rechte müssen UID/GID 10001 lesenden Zugriff ermöglichen.

```sh
docker compose -f compose.yml -f compose.nfs.yml up -d --build
```

In der GUI Download-Quelle **NFS / SMB** wählen und den **Quellpfad auf dem Emby-Server** setzen. Beispiel: Emby liefert `/mnt/movies/Film/a.mkv`, NFS exportiert dessen Inhalt → SourcePrefix `/mnt/movies` → Containerdatei `/media/Film/a.mkv`.

Docker führt den NFS-Mount aus. Die Webanwendung hat weder Root-Rechte noch Zugriff auf den Docker-Socket. Bei Änderung von NFS-Volume-Optionen ein neues Volume verwenden; bestehende Volume-Optionen werden von Docker nicht automatisch ersetzt. Keine pauschale Volume-Löschung.

## SMB

Auf dem Docker-Host `cifs-utils` bereitstellen. Eine rootgeschützte Datei `/etc/vloader-smb.credentials` mit `username=...`, `password=...` und optional `domain=...` erstellen; Modus 0600.

```sh
sudo scripts/mount-smb.sh //server/share /mnt/vloader-media /etc/vloader-smb.credentials
# .env: MEDIA_PATH=/mnt/vloader-media
docker compose -f compose.yml -f compose.smb.yml up -d --build
```

In der GUI Download-Quelle **NFS / SMB** und passendes SourcePrefix speichern. Für dauerhafte Host-Mounts eine entsprechende systemd-/fstab-Konfiguration einrichten. Das Skript mountet schreibgeschützt mit SMB 3.1.1. SMB-Passwörter werden nicht in Docker-Volume-Optionen oder im Webformular gespeichert. Der Host-Mount wurde ohne deine Serverdaten nicht ausgeführt.

## OIDC

In `.env` setzen:

```dotenv
OIDC_ISSUER=https://id.example.com/realms/media
OIDC_CLIENT_ID=vloader
OIDC_CLIENT_SECRET=
OIDC_ALLOWED_SUBJECTS=stable-subject-id-1,stable-subject-id-2
```

Redirect-URI beim Provider: `${APP_URL}/auth/callback`. Authorization Code Flow mit PKCE S256 und `openid profile` aktivieren. Bei einem vertraulichen Client das Client-Secret setzen. Die erlaubten Werte sind die stabilen `sub`-Claims, nicht E-Mail-Adressen. Nach Änderungen Container mit `docker compose up -d --force-recreate` neu erstellen. OIDC ist erst sichtbar, wenn eingerichtet; fehlerhafte Discovery verhindert einen irreführend funktionierenden Start.

Alle zugelassenen Benutzer besitzen Administratorrechte und können die gesamte konfigurierte Sammlung sehen. Es gibt keine Übernahme individueller Emby-Benutzerrechte. Siehe [Security.md](Security.md).

## Konfiguration und Vorlagen

- `compose.yml`: lokale Entwicklung, non-root und Read-only-Härtung.
- `compose-template.yml`: Standardvorlage; `compose-templte.yml`: zusätzlich der angefragte Dateiname.
- `.env.example`: kommentierte Variablen; `.enx.example`: zusätzlich der angefragte Dateiname.
- `Security.md`, `SECURITY.md`: Sicherheitshinweise und Meldeverfahren.
- `docs/`: Architektur und Teststrategie.

Gespeicherte GUI-Einstellungen überschreiben Emby-/Quellen-Startwerte aus `.env`; Änderungen danach über die GUI vornehmen. OIDC und lokale Zugangsdaten bleiben ausschließlich Umgebungswerte. Ein Serverwechsel verlangt die erneute Eingabe eines API-Schlüssels und eine neue Synchronisierung.

## Entwicklung und Tests

Mit Go 1.27.1 oder neuer:

```sh
go test ./...
go test -race ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Ohne Go auf dem Host:

```sh
docker run --rm -v "$PWD:/app" -w /app golang:1.27.1-alpine sh -c 'go test ./... && go vet ./...'
# Race-Detector benötigt zusätzlich einen C-Compiler, z. B. apk add --no-cache build-base.
```

CI führt Tests, Race-Detector, vet, Schwachstellenprüfung, Compose-Validierung und Image-Build aus. Die separate Action **Build Docker image** veröffentlicht Images und Releases ausschließlich für Release-Tags. Externe Integrationen separat mit echten Servern abnehmen; siehe [Teststrategie](docs/TESTING-STRATEGY.md).


## GitHub Actions

### Build Docker image

Bei Push auf `main` und Pull Requests wird das Image gebaut und geprüft, ohne es zu veröffentlichen. Die manuelle Ausführung ohne `release_tag` ist ebenfalls ein reiner Testbuild.

Releases verwenden wie bei caddymgm folgende Tags:

| Git-Tag | GHCR-Tags |
| --- | --- |
| `v0.1` | `ghcr.io/thetaran/vloader:0.1`, `:latest`, `:sha-<commit>` |
| `v0.1.1` | `ghcr.io/thetaran/vloader:0.1.1`, `:0.1`, `:sha-<commit>` |

Vor dem Taggen müssen die Release Notes als `.github/release-notes/<tag>.md` im Release-Commit vorliegen. Anschließend den ausdrücklich gewählten Git-Tag pushen. Der Workflow prüft den Code, veröffentlicht das Image, verifiziert die Registry-Tags und erstellt den GitHub Release. Bereits vorhandene GitHub Releases bleiben unverändert.

Ein bestehender Tag kann über **Run workflow → release_tag** erneut verarbeitet werden. Dabei wird exakt der Quellstand dieses Tags ausgecheckt, auch wenn die manuelle Ausführung auf `main` gestartet wurde. Der Workflow erzeugt keine Git-Tags. Der erste Image-Release ist erst nach einem Release-Tag verfügbar.

### Check component versions

Läuft montags um 06:17 UTC, manuell und bei Änderungen der überwachten Dateien auf `main`. Vergleicht Go in `go.mod` und Dockerfile, den Alpine-Laufzeitzweig sowie sämtliche in `go.mod` gepinnten direkten und indirekten Module mit den stabilen Upstream-Versionen. Ein Alpine-Minor-Tag folgt Patch-Updates automatisch; gemeldet wird der nächste stabile Minor-Zweig.

Bei Updates wird genau ein markiertes Issue **Component updates available** erstellt oder aktualisiert; sind alle Komponenten aktuell, wird es geschlossen. Ein Fehler beim Abrufen der Versionen bricht den Lauf ab, ohne ein vorhandenes Issue fälschlich zu schließen. Versionsänderungen, Releases und Image-Publishing erfolgen nicht durch diese Prüfung.

Lokal ohne GitHub-Schreibzugriff testen:

```sh
python3 scripts/check-component-versions.py
python3 -m unittest discover -s scripts -p 'test_*.py'
```

Die Actions verwenden den von GitHub bereitgestellten `GITHUB_TOKEN`; zusätzliche Registry-Passwörter sind nicht erforderlich. Der Build benötigt `packages: write`, die Release-Erstellung `contents: write` und die Versionsprüfung `issues: write`.

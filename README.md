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

## NFS und SMB im Hauptimage

Das Hauptimage enthält `nfs-utils`, `cifs-utils` und das Startskript für beide Dateiquellen. Es gibt keine separaten NFS-/SMB-Container oder Compose-Overlays mehr.

Alle Varianten stehen kommentiert in **compose-template.yml**. Die tatsächlich verwendete Variante wird direkt in **compose.yml** eingetragen. Für Downloads über die Emby-API bleibt die Standardkonfiguration ausreichend.

### Freigabe aktivieren

1. Den lokalen `/media`-Bind-Mount aus `services.vloader.volumes` entfernen; das Datenvolume beibehalten.
2. Die gemeinsamen Mount-Einstellungen aus der Vorlage übernehmen: Startbenutzer `0:0`, `SYS_ADMIN`, `SETUID`, `SETGID` und das dort angegebene `security_opt`.
3. Genau eine `environment`-Variante übernehmen: `SOURCE_MOUNT: nfs` oder `SOURCE_MOUNT: smb`.
4. NFS-Server und Export beziehungsweise SMB-Server und Freigabe sowie die Mount-Credentials in `.env` eintragen. Für SMB zusätzlich die vollständige SMB-`cap_add`-Zeile (einschließlich `DAC_READ_SEARCH`, `DAC_OVERRIDE`) und die Secret-Definition aus der Vorlage in `compose.yml` übernehmen.
5. `docker compose up -d --build --force-recreate` ausführen. Die Download-Quelle und der Emby-Quellpfad werden anschließend unter **Settings → Connection** gespeichert; diese Werte gehören nicht in `.env`.

Der Startprozess mountet schreibgeschützt nach `/media` und wechselt anschließend auf UID/GID `10001:10001`. Bei einem Mount-Fehler startet die Anwendung nicht. Ein bereits belegtes `/media` wird nicht übermountet. Die Webanwendung führt keine Mount-Befehle aus.

### NFS

`NFS_SERVER` ist ein Hostname oder eine IPv4-Adresse; `NFS_EXPORT` der absolute Exportpfad. NFSv4 über TCP wird verwendet. Beispiel: Emby liefert `/mnt/movies/Film/a.mkv`, der Export enthält `Film/a.mkv` → SourcePrefix `/mnt/movies` → Containerdatei `/media/Film/a.mkv`. Die Freigabe muss UID/GID 10001 lesenden Zugriff geben.

### SMB

`SMB_SERVER` ist ein Hostname oder eine IPv4-Adresse; `SMB_SHARE` der Freigabename. SMB 3.1.1 wird verwendet. Setze `SMB_CREDENTIAL_USERNAME` und `SMB_CREDENTIAL_PASSWORD` in der lokalen `.env`; das Startskript schreibt sie nur während des Mounts in eine temporäre Datei mit Modus 0600 und entfernt sie danach. Die Werte gehören nie ins Repository oder Image.

Bei Domänenkonten kann der Benutzer im Format `DOMAIN\\username` angegeben werden, zum Beispiel `ALONSO\\SMBvloader`. Das Startskript schreibt diesen Wert für `mount.cifs` als getrennte `domain=`- und `username=`-Einträge. Bei lokalen NAS-Konten genügt der reine Benutzername. Ein Emby-Benutzer ist nicht automatisch ein gültiges NAS-SMB-Konto.

Es ist kein manuelles Mounten auf dem Host erforderlich. Der Docker-Host muss jedoch Kernel-Unterstützung für NFS beziehungsweise CIFS bereitstellen. Die optionalen Mount-Capabilities gelten nur für die konfigurierte Freigabe; der Webprozess läuft ohne Root-Rechte. Ohne NFS/SMB-Aktivierung sind diese zusätzlichen Rechte nicht nötig.

## OIDC

In `.env` setzen:

```dotenv
OIDC_ISSUER=https://id.example.com/realms/media
OIDC_CLIENT_ID=vloader
OIDC_CLIENT_SECRET=
OIDC_ALLOWED_SUBJECTS=stable-subject-id-1,stable-subject-id-2
```

Redirect-URI beim Provider: `${APP_URL}/auth/callback`. Authorization Code Flow mit PKCE S256 und `openid profile` aktivieren. Bei einem vertraulichen Client das Client-Secret setzen. Die erlaubten Werte sind die stabilen `sub`-Claims, nicht E-Mail-Adressen. Die Werte können danach auch im Bereich **Settings → Authentication** gepflegt werden; das Secret wird niemals an den Browser zurückgegeben.

Alle zugelassenen Benutzer besitzen Administratorrechte und können die gesamte konfigurierte Sammlung sehen. Es gibt keine Übernahme individueller Emby-Benutzerrechte. Siehe [SECURITY.md](SECURITY.md).

## Konfiguration und Vorlagen

- `compose.yml`: lokale Entwicklung, non-root und Read-only-Härtung.
- `compose-template.yml`: Standardvorlage einschließlich NFS-/SMB-Konfiguration.
- `.env.example`: kommentierte Variablen; `.enx.example`: zusätzlich der angefragte Dateiname.
- `Security.md`, `SECURITY.md`: Sicherheitshinweise und Meldeverfahren.
- `vloader/docs/`: Architektur und Teststrategie.

Gespeicherte GUI-Einstellungen überschreiben Emby-/Quellen-Startwerte aus `.env`; Änderungen danach über die GUI vornehmen. Lokale Anmeldung wird über `LOCAL_AUTH_ENABLED=true|false` in Compose gesteuert. Bei `false` muss eine vollständige OIDC-Konfiguration vorhanden sein. Ein Serverwechsel verlangt die erneute Eingabe eines API-Schlüssels und eine neue Synchronisierung.

## Entwicklung und Tests

Mit Go 1.27.1 oder neuer im Quellverzeichnis:

```sh
cd vloader
go test ./...
go test -race ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Ohne Go auf dem Host:

```sh
docker run --rm -v "$PWD/vloader:/app" -w /app golang:1.27.1-alpine sh -c 'go test ./... && go vet ./...'
# Race-Detector benötigt zusätzlich einen C-Compiler, z. B. apk add --no-cache build-base.
```

CI führt Tests, Race-Detector, vet, Schwachstellenprüfung, Compose-Validierung und Image-Build aus. Die separate Action **Build Docker image** veröffentlicht Images und Releases ausschließlich für Release-Tags. Externe Integrationen separat mit echten Servern abnehmen; siehe [Teststrategie](vloader/docs/TESTING-STRATEGY.md).


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
python3 vloader/scripts/check-component-versions.py
python3 -m unittest discover -s vloader/scripts -p 'test_*.py'
```

Die Actions verwenden den von GitHub bereitgestellten `GITHUB_TOKEN`; zusätzliche Registry-Passwörter sind nicht erforderlich. Der Build benötigt `packages: write`, die Release-Erstellung `contents: write` und die Versionsprüfung `issues: write`.

## Projektstruktur

```text
compose.yml                # aktive Deployment-Konfiguration
compose-template.yml       # vollständige Vorlage inkl. NFS/SMB
.env / .env.example        # lokale Werte / Beispiel
.github/workflows/         # GitHub Actions
vloader/
  Dockerfile               # Hauptimage
  .dockerignore
  go.mod / go.sum
  cmd/                     # Go-Einstiegspunkt
  internal/                # Backend und eingebettete WebGUI
  docker/entrypoint.sh     # optionale NFS-/SMB-Mounts beim Start
  scripts/                 # Versionsprüfung und Entwicklungstests
  docs/                    # Architektur und Teststrategie
```

Alle Compose-Befehle werden im Repository-Root ausgeführt. Docker baut ausschließlich aus `./vloader`; lokale Einstellungen und Betriebsdaten liegen außerhalb dieses Build-Kontexts.

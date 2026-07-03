# Deploy WatchTower ke Oracle Cloud Free Tier (ARM64)

Panduan ini men-deploy seluruh stack WatchTower (MySQL, Redis, API, scheduler,
frontend, dan nginx+TLS) ke satu VM ARM Ampere A1 di Oracle Cloud Free Tier
menggunakan Docker Compose.

## 1. Buat VM

Di OCI Console: **Compute → Instances → Create Instance**.

- **Name**: `watchtower-prod` (bebas)
- **Image**: Canonical Ubuntu 22.04 (pilih varian **aarch64**, bukan x86_64)
- **Shape**: `VM.Standard.A1.Flex` (Ampere ARM) — set **4 OCPU** dan **24 GB
  memory**. Ini adalah alokasi maksimum yang termasuk Always Free tier
  (total 4 OCPU / 24 GB Ampere A1 per akun, bisa dipakai di 1 VM atau
  dipecah ke beberapa VM).
- **Networking**: gunakan VCN default (atau buat baru), pastikan **Assign a
  public IPv4 address** dicentang.
- **SSH keys**: upload public key kamu sendiri (atau biarkan OCI generate
  key pair baru dan download private key-nya).

Setelah instance running, catat **Public IP**-nya.

## 2. Buka port 22, 80, 443 di Security List

Instance ARM Ampere secara default menggunakan Ubuntu's `iptables`/`netfilter`
DAN OCI Security List — keduanya harus mengizinkan trafik masuk.

**Di OCI Console**: buka VCN instance kamu → **Security Lists** → default
security list → **Add Ingress Rules**, tambahkan 3 rule (masing-masing
`Source CIDR: 0.0.0.0/0`, `IP Protocol: TCP`):

| Destination Port | Keterangan |
|---|---|
| 22 | SSH |
| 80 | HTTP (redirect ke HTTPS + ACME challenge certbot) |
| 443 | HTTPS |

**Di dalam VM itu sendiri**, Ubuntu 22.04 pada image OCI biasanya sudah
mengizinkan port ini secara default via `iptables`, tapi verifikasi:

```bash
sudo iptables -L INPUT -n --line-numbers
```

Kalau port 80/443 belum ada rule ACCEPT, tambahkan:

```bash
sudo iptables -I INPUT -p tcp --dport 80 -j ACCEPT
sudo iptables -I INPUT -p tcp --dport 443 -j ACCEPT
sudo netfilter-persistent save
```

## 3. Install Docker + docker-compose-plugin

SSH ke VM (`ssh ubuntu@<PUBLIC_IP>`), lalu:

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl gnupg

sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg

echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# Run docker without sudo (log out/in afterwards for this to take effect)
sudo usermod -aG docker $USER
```

This installs the **`docker compose`** (v2, space, plugin) command used
throughout this project's Makefile — not the deprecated standalone
`docker-compose` (hyphenated v1) binary, which Ubuntu no longer packages.

Verify:

```bash
docker --version
docker compose version
```

## 4. Clone repo + setup .env

```bash
git clone https://github.com/<your-username>/watchtower.git
cd watchtower

cp .env.example .env
cp backend/.env.example backend/.env
cp frontend/.env.example frontend/.env
```

Edit **`.env`** (root) — konfigurasi database/redis untuk container:

```
DB_ROOT_PASSWORD=<strong-random-password>
DB_NAME=watchtower
DB_USER=watchtower
DB_PASSWORD=<strong-random-password>
REDIS_PASSWORD=<strong-random-password>
DOMAIN=your-domain.com
SSL_EMAIL=you@example.com
```

Edit **`backend/.env`** — konfigurasi aplikasi. Beberapa catatan penting:

- `DB_HOST`, `DB_PORT`, `REDIS_HOST`, `REDIS_PORT`, `DB_USER`,
  `DB_PASSWORD`, `DB_NAME`, `REDIS_PASSWORD` di file ini **diabaikan** saat
  jalan lewat Docker Compose — `docker-compose.yml` meng-override
  variable-variable ini supaya container selalu connect ke service
  `mysql`/`redis` di Docker network dengan kredensial dari `.env` root
  (lihat komentar di `docker-compose.yml`). Kamu tetap boleh isi
  field-field ini (berguna kalau suatu saat run `go run ./cmd/api` di luar
  Docker), tapi tidak wajib untuk deployment ini.
- **`FRONTEND_URL`**: set ke `https://your-domain.com` — dipakai
  `CORSMiddleware` saat `APP_ENV=production` untuk membatasi origin yang
  diizinkan.
- Isi `JWT_SECRET` (random string panjang), `DEEPSEEK_API_KEY`,
  `TELEGRAM_ASSET_BOT_TOKEN`, `TELEGRAM_SENTINEL_BOT_TOKEN`,
  `TWELVE_DATA_API_KEY` sesuai kredensial kamu.
- `TELEGRAM_MODE` di sini boleh dibiarkan `polling` — `docker-compose.prod.yml`
  akan meng-override jadi `webhook` otomatis untuk stack production (lihat
  langkah 7).

Frontend's `.env` tidak dipakai runtime di dalam container (Vite build-time
only) — nilai default di `frontend/Dockerfile` (`/api` relatif) sudah benar
untuk setup reverse-proxy ini, jadi biasanya tidak perlu diubah.

## 5. Build & jalankan stack

```bash
make prod-build
make prod-up
```

`make prod-up` menjalankan `mysql`, `redis`, `api`, `scheduler`, `frontend`,
`nginx`, dan `certbot`. **nginx akan gagal start di titik ini** kalau
sertifikat TLS belum ada — lanjut ke langkah 6 dulu sebelum menganggap stack
ini "up" sepenuhnya (atau lakukan langkah 6 terlebih dahulu, lalu baru
`make prod-up`).

Migrasi database **otomatis berjalan** setiap kali container `api` atau
`scheduler` start (lihat `internal/db.DB.RunMigrations`, dipanggil dari
`cmd/api/main.go` dan `cmd/scheduler/main.go`) — kamu tidak perlu menjalankan
apa pun secara manual untuk ini. `make db-migrate` tersedia sebagai opsi
tambahan (menjalankan migrasi dari host, di luar container), tapi ini
memerlukan Go ter-install di VM (`sudo snap install go --classic` atau
sejenisnya) dan **tidak diperlukan** untuk alur deployment standar di atas.

## 6. Setup SSL dengan certbot

nginx (`nginx/nginx.prod.conf`) mengharapkan sertifikat sudah ada di
`nginx/ssl/live/your-domain.com/` sebelum bisa start dengan HTTPS aktif —
masalah klasik "ayam-telur" certbot+nginx. Solusinya: buat sertifikat
self-signed sementara dulu supaya nginx bisa boot, lalu ganti dengan
sertifikat asli dari Let's Encrypt.

**Ganti dulu** setiap `your-domain.com` di `nginx/nginx.prod.conf` dengan
domain asli kamu (harus sama dengan `DOMAIN` di `.env`):

```bash
sed -i "s/your-domain.com/$(grep ^DOMAIN= .env | cut -d= -f2)/g" nginx/nginx.prod.conf
```

Lalu buat sertifikat dummy supaya nginx bisa start:

```bash
DOMAIN=$(grep ^DOMAIN= .env | cut -d= -f2)
mkdir -p nginx/ssl/live/$DOMAIN
openssl req -x509 -nodes -days 1 -newkey rsa:2048 \
  -keyout nginx/ssl/live/$DOMAIN/privkey.pem \
  -out nginx/ssl/live/$DOMAIN/fullchain.pem \
  -subj "/CN=$DOMAIN"
```

Start stack-nya (nginx sekarang bisa boot dengan sertifikat dummy):

```bash
make prod-up
```

Pastikan DNS domain kamu sudah menunjuk ke Public IP VM ini sebelum lanjut —
certbot butuh domain benar-benar reachable dari internet untuk validasi
HTTP-01. Lalu minta sertifikat asli:

```bash
DOMAIN=$(grep ^DOMAIN= .env | cut -d= -f2)
SSL_EMAIL=$(grep ^SSL_EMAIL= .env | cut -d= -f2)

docker compose -f docker-compose.yml -f docker-compose.prod.yml run --rm certbot \
  certonly --webroot -w /var/www/certbot \
  -d "$DOMAIN" --email "$SSL_EMAIL" --agree-tos --no-eff-email
```

Reload nginx supaya memakai sertifikat asli yang baru saja didapat:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml exec nginx nginx -s reload
```

Sertifikat akan auto-renew setiap 12 jam oleh service `certbot` (lihat
`entrypoint` di `docker-compose.prod.yml`) — nginx sendiri tidak otomatis
reload setelah renewal, jadi untuk deployment jangka panjang tambahkan cron
job di host yang menjalankan perintah `exec nginx nginx -s reload` di atas
secara berkala (mis. mingguan).

## 7. Switch Telegram dari polling ke webhook

Stack production (`docker-compose.prod.yml`) sudah men-set `TELEGRAM_MODE=webhook`
pada service `api`, yang membuatnya mendaftarkan route
`POST /webhook/telegram/{asset,sentinel}` alih-alih long-polling. Beritahu
Telegram untuk mengirim update ke webhook tersebut, untuk **kedua** bot:

```bash
DOMAIN=$(grep ^DOMAIN= .env | cut -d= -f2)
ASSET_BOT_TOKEN=<isi dari backend/.env: TELEGRAM_ASSET_BOT_TOKEN>
SENTINEL_BOT_TOKEN=<isi dari backend/.env: TELEGRAM_SENTINEL_BOT_TOKEN>

curl -s "https://api.telegram.org/bot${ASSET_BOT_TOKEN}/setWebhook?url=https://${DOMAIN}/webhook/telegram/asset"
curl -s "https://api.telegram.org/bot${SENTINEL_BOT_TOKEN}/setWebhook?url=https://${DOMAIN}/webhook/telegram/sentinel"
```

Setiap respons harus `{"ok":true,"result":true,...}`. Verifikasi kapan saja
dengan `getWebhookInfo`:

```bash
curl -s "https://api.telegram.org/bot${ASSET_BOT_TOKEN}/getWebhookInfo"
```

## 8. Verifikasi

```bash
curl https://your-domain.com/health
```

> **Catatan**: endpoint health check ada di `/health` langsung — **bukan**
> `/api/v1/health` atau bahkan `/api/health`. Backend WatchTower tidak
> memakai versioning path sama sekali (semua endpoint lain ada di bawah
> `/api/...` tanpa `/v1`), dan `/health` sendiri terdaftar di luar grup
> `/api` (lihat `cmd/api/main.go`: `router.GET("/health", ...)`).

Respons sehat:

```json
{"status":"ok","db":"ok","redis":"ok"}
```

Kalau `status` adalah `"degraded"`, cek log:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs -f api
```

Terakhir, buka `https://your-domain.com` di browser untuk memastikan
frontend ter-load dan bisa register/login — ini juga membuktikan reverse
proxy nginx meneruskan `/api/*` dengan benar ke service `api`.

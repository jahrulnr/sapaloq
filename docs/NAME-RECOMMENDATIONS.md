# Nama produk — assessment & rekomendasi

> **Keputusan (2026-06-19):** produk resmi **SapaLOQ** — repo `/apps/workspace/sapaloq`, binary `sapaloq-core` / `sapaloq-widget`, socket `sapaloq.sock`, config `~/.config/sapaloq/`.
> Dokumen di bawah = arsip assessment sebelum lock nama.
> Last updated: 2026-06-19

Related: [VISION.md](./VISION.md) · [UI-DECISION.md](./UI-DECISION.md) · M5a spike

---

## Kenapa "SapaLOQ" terasa kurang?

| Aspek | SapaLOQ memberi | Yang produk kita sebenarnya jual |
|-------|-----------------|----------------------------------|
| Metaphor | Wadah tertutup, menyimpan, klinis | **Kehadiran** di desktop — orb mengambang, ngobrol, ingat |
| Feel | Storage container, pill, space sapaloq | Teman yang *nongkrong* di pojok layar |
| UX spike | — | FAB → popup "Hai 👋", ring bernapas, bukan "buka aplikasi" |
| Arsitektur | `sapaloq-core` masuk akal secara teknis | User tidak mikir "core + container" |

**SapaLOQ tidak salah** — cocok untuk *memory isolation* dan *single binary*. Kurangnya di **warmth + presence** yang sudah kita validasi di M5a (orb, FAB, companion chat).

---

## Kriteria nama (dari assessment)

Wajib:

1. **Companion, bukan IDE** — bukan Cursor clone, bukan terminal
2. **Singkat** — 1–2 suku kata; cocok untuk FAB kecil & CLI (`xxx-core`)
3. **Globally pronounceable** — ID + EN tanpa awkward
4. **Beda dari** cursor-agent, Copilot, Claw, 9router

Nice-to-have:

5. Mengisyaratkan **presence** (mengambang, menemani) atau **memory** (ingat)
6. Boleh ada nuansa **orb/ring** tanpa terlalu literal
7. Domain / npm / GitHub masih realistis (soft check — tidak exhaustive)

Hindari:

- Nama generik AI (`Assistant`, `Copilot`, `Mind`)
- Terlalu cute untuk audience dev (`Buddy`, `Pal`)
- Terlalu keras (`Sentinel`, `Overwatch`)

---

## Tier A — rekomendasi utama

### 1. **Perch** ⭐

| | |
|-|-|
| **Feel** | Companion yang "bertengger" di pojok layar — persis FAB spike |
| **Pros** | Visual cocok; memorable; `perch-core` / `~/.config/perch/` enak |
| **Cons** | Beberapa produk pakai "Perch" (cek domain) |
| **Tagline** | *Desktop companion that perches on your screen.* |

### 2. **Ember**

| | |
|-|-|
| **Feel** | Orb menyala, ring bernapas — hangat, hidup, tidak klinis |
| **Pros** | Kuat visually; gender-neutral; cocok neon blue glow |
| **Cons** | Agak generic di creative tools |
| **Tagline** | *A glowing companion on your desktop.* |

### 3. **Sapa**

| | |
|-|-|
| **Feel** | Popup spike: "Hai 👋" — menyapa dulu, bukan execute task |
| **Pros** | Unik di space dev tools; akar Indonesia; 4 huruf |
| **Cons** | Perlu edukasi arti di market global ("sa-pa") |
| **Tagline** | *Companion yang menyapa — ingat, bantu, delegasi.* |

### 4. **Sidecar**

| | |
|-|-|
| **Feel** | Sesi paralel + handoff ke worker — arsitektur persis assessment |
| **Pros** | Dev audience langsung paham metaphor |
| **Cons** | Agak panjang; lebih "engineering" daripada "companion feel" |
| **Tagline** | *The sidecar for your desktop — memory here, coding handoff there.* |

---

## Tier B — solid alternatif

| Nama | Angle | Catatan |
|------|-------|---------|
| **Iring** | ID: menemani | Distinctive; pronunciation perlu sekali jelasin |
| **Orbit** | Sudah di VISION (astronaut/orbit) | Visual konsisten; banyak produk "Orbit" |
| **Rem** | Remember + ringkasan singkat | `rem-core` bagus; konotasi sleep/REM |
| **Pip** | Small floating helper | Approachable; mungkin terlalu playful |
| **Familiar** | Mitologi: companion spirit | Perfect lore; panjang & niche |
| **Lodestar** | Panduan di desktop | Elegan; agak poetic untuk CLI tool |
| **Glint** | Kilau di pojok | Visual; kurang carry "memory" |

---

## Tier C — tetap pakai SapaLOQ?

| Skenario | Rekomendasi |
|----------|-------------|
| Fokus shipping M1–M5b | **SapaLOQ** sebagai codename repo cukup — rename later |
| Rebrand sebelum public beta | Pilih Tier A sekarang; migration path di bawah |
| Hybrid | **SapaLOQ** = repo/binary internal; **Perch/Ember** = user-facing |

---

## Mapping rename (kalau ganti)

| SapaLOQ today | Contoh → Perch |
|---------------|----------------|
| `sapaloq-core` | `perch-core` |
| `~/.config/sapaloq/` | `~/.config/perch/` |
| `sapaloq.sock` | `perch.sock` |
| `companion.db` | bisa tetap atau `perch.db` |
| Topic prefix `sapaloq.v1` | `perch.v1` (breaking — defer pre-M1) |

Rename **paling murah** sebelum M1 (paths + schema belum di production).

---

## Rekomendasi tim (satu kalimat)

Kalau "SapaLOQ kurang" karena terasa **dingin & container-like**, pilihan paling align dengan M5a spike:

> **Perch** (presence di pojok) atau **Ember** (orb hidup) untuk global; **Sapa** kalau mau identitas Indonesia yang masih singkat.

**Sidecar** kalau audience utama developer dan mau tekankan handoff architecture.

---

## Poll cepat (pilih satu arah)

| Kalau kamu mau… | Pilih |
|-----------------|-------|
| Feel FAB di pojok | **Perch** |
| Feel orb menyala / ring | **Ember** |
| Feel "Hai companion" + lokal | **Sapa** |
| Feel arsitektur handoff | **Sidecar** |
| Tidak rename dulu | **SapaLOQ** (codename) |

Tidak ada yang wajib sekarang — M5b bisa lanjut di bawah SapaLOQ; rename path table siap kalau kamu lock.

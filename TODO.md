# gleann-plugin-docs — Geliştirme Planı

## Docling Entegrasyonu

### Karşılaştırma: MarkItDown vs Docling

| Özellik | MarkItDown | Docling |
|---|---|---|
| **Hız (CPU, sayfa başı)** | ~0.01s | ~3.1s ortalama (0.6–16.3s) |
| **Tablo doğruluğu** | Düşük (basit metin çıkarma) | %97.9 (TableFormer modeli) |
| **OCR desteği** | Yok | Var (EasyOCR / Tesseract) |
| **Layout analizi** | Yok | Var (DocLayNet modeli) |
| **Kurulum boyutu** | ~50 MB | 500+ MB (PyTorch bağımlılığı) |
| **RAM kullanımı** | <500 MB | 4–8 GB |
| **Desteklenen formatlar** | PDF, DOCX, XLSX, PPTX, resimler | PDF, DOCX, PPTX, resimler, HTML |
| **GPU gereksinimi** | Yok | Opsiyonel (CPU yeterli) |

### CPU Performans Detayları (Docling)

- **Medyan:** 0.79s/sayfa
- **Ortalama:** 3.1s/sayfa (karmaşık tablolar yüzünden yüksek)
- **Aralık:** 0.6s (basit metin) – 16.3s (yoğun tablo)
- **Platform:** x86_64, PyTorch CPU backend
- Basit metin sayfaları hızlı, tablo ağırlıklı sayfalar yavaş

### Öneri: Akıllı Yönlendirme (Smart Routing)

**PDF → Docling, diğer tüm formatlar → MarkItDown**

Gerekçe:
- MarkItDown DOCX/XLSX/PPTX için yeterli ve çok hızlı
- Docling'in asıl gücü PDF'lerde: OCR, tablo çıkarma, layout analizi
- Her iki backend tek serviste birlikte çalışabilir
- Kullanıcı `DOCLING_ENABLED=false` ile devre dışı bırakabilir

### Geliştirme Planı

#### Faz 1: Docling Backend Ekleme
- [ ] `requirements.txt`'e `docling` bağımlılığı ekle (opsiyonel grup)
- [ ] `docling_backend.py` modülü oluştur
  - `convert_pdf(file_path) -> str` fonksiyonu
  - Tablo, başlık, paragraf yapısını markdown'a çevir
  - Hata durumunda MarkItDown'a fallback
- [ ] Ortam değişkeni: `DOCLING_ENABLED` (varsayılan: `true` eğer yüklüyse)

#### Faz 2: Akıllı Yönlendirme
- [ ] `/convert` endpoint'inde uzantıya göre routing:
  - `.pdf` → Docling (yüklüyse), fallback MarkItDown
  - `.docx`, `.xlsx`, `.pptx`, `.csv`, resimler → MarkItDown
- [ ] Konfigürasyon: `DOCLING_FORMATS=pdf` (hangi formatlar Docling'e gider)
- [ ] Health endpoint'e backend bilgisi ekle: `{"backends": ["markitdown", "docling"]}`

#### Faz 3: Performans Optimizasyonu
- [ ] Docling model'ini lazy-load et (ilk PDF isteğinde yükle)
- [ ] Sayfa sayısı eşiği: >50 sayfa PDF'ler için progress logging
- [ ] Timeout ayarı: uzun PDF'ler için gleann tarafında timeout artırımı değerlendir
- [ ] Opsiyonel: OCR'ı sadece taranmış PDF'ler için aktifleştir (hız kazancı)

#### Faz 4: Test ve Dokümantasyon
- [ ] Birim testleri: PDF routing, fallback davranışı, hata durumları
- [ ] Entegrasyon testi: `gleann build` ile PDF indeksleme
- [ ] README güncelle: Docling kurulum adımları, konfigürasyon seçenekleri
- [ ] Benchmark: farklı PDF tipleri için karşılaştırmalı sonuçlar

### Kurulum Notları

```bash
# Temel kurulum (sadece MarkItDown)
pip install -r requirements.txt

# Docling ile kurulum (PDF kalitesi için)
pip install -r requirements-docling.txt
# veya
pip install docling
```

### Risk ve Dikkat Edilecekler

- **İlk yükleme süresi:** Docling modelleri ilk çalıştırmada ~1-2 GB indiriyor (cache'lenir)
- **RAM:** 4-8 GB gereksinim, düşük RAM'li sistemlerde sorun olabilir
- **PyTorch boyutu:** CPU-only PyTorch bile ~500 MB, Docker image boyutunu artırır
- **gleann timeout:** `plugin.go:214` → 30s timeout, uzun PDF'ler için yetmeyebilir

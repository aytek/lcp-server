# itech Books — Custom DRM Implementation Plan

## Genel Bakış

EDRLab LCP yerine kendi custom DRM sistemimizi kuruyoruz.
- Maliyet: Sıfır (mevcut altyapıya eklenti)
- Ekosistem: Tamamen kapalı (sadece itech Books)
- Yeni server: Yok (Railway Go server'a endpoint eklenir)

---

## Mimari

```
Firebase Auth  +  Firebase Storage (şifreli EPUB'lar)
        ↓
Railway Go Server (license API)
        ↓
iOS App
  ├── Firebase Auth
  ├── Custom ContentProtection (Readium Swift Toolkit)
  └── Keychain (license cache)
```

---

## Klasör Yapısı (Firebase Storage)

```
/books/
  {contentId}/
    encrypted.epub      ← AES-256 şifreli EPUB
    meta.json           ← içerik metadata (IV, algorithm vb.)
```

`meta.json` örneği:
```json
{
  "contentId": "kitap-slug",
  "title": "Kitap Adı",
  "encryptedAt": "2026-04-11T00:00:00Z",
  "algorithm": "AES-256-CBC",
  "iv": "base64-encoded-iv"
}
```

Firebase Storage Security Rules: şifreli EPUB'lara direkt erişim kapalı, sadece server üzerinden indirilir.

---

## Veri Yapıları

### License JSON (server → iOS)

```json
{
  "id": "uuid-v4",
  "userId": "firebase-uid",
  "contentId": "kitap-slug",
  "contentKey": "base64-encoded-aes256-key",
  "downloadUrl": "https://...",
  "issued": "2026-04-11T00:00:00Z",
  "expires": "2027-04-11T00:00:00Z",
  "signature": "hmac-sha256-hex"
}
```

> `contentKey`: İçeriği şifreleyen AES-256 anahtarı.  
> `downloadUrl`: Firebase Storage signed URL (kısa süreli, server tarafından üretilir).  
> `signature`: HMAC-SHA256 ile imzalanmış bütünlük kontrolü.

---

## Bölüm 1 — EPUB Şifreleme (Upload Pipeline)

Bir kitap sisteme eklenirken çalışacak Go script veya endpoint.

### Adımlar

1. Ham EPUB'u al
2. Rastgele AES-256 key + IV üret
3. EPUB'u AES-256-CBC ile şifrele
4. Şifreli dosyayı Firebase Storage'a yükle: `/books/{contentId}/encrypted.epub`
5. `meta.json` üret ve Storage'a yükle
6. AES key'ini güvenli DB'ye kaydet (Railway'deki DB, asla Storage'a yazılmaz)

```go
func encryptEPUB(data []byte) (encrypted []byte, key []byte, iv []byte, err error) {
    key = make([]byte, 32) // AES-256
    iv  = make([]byte, 16)
    rand.Read(key)
    rand.Read(iv)

    block, _ := aes.NewCipher(key)
    // PKCS7 padding + CBC encrypt
    padded := pkcs7Pad(data, aes.BlockSize)
    encrypted = make([]byte, len(padded))
    cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)
    return
}
```

---

## Bölüm 2 — Go Server: License API

Railway'deki mevcut Go server'a eklenecek endpoint'ler.

### Endpoint'ler

```
POST   /licenses              → Yeni lisans oluştur
GET    /licenses/:id          → Lisans doğrula + getir
POST   /licenses/:id/revoke   → İptal et (opsiyonel)
```

---

### POST /licenses

**Request:**
```json
{
  "userId": "firebase-uid",
  "contentId": "kitap-slug",
  "firebaseToken": "firebase-jwt"
}
```

**İşlem sırası:**
1. `firebaseToken` doğrula (Firebase Admin SDK)
2. `userId + contentId` için aktif lisans zaten var mı kontrol et
3. DB'den `contentKey` çek
4. Firebase Storage Admin SDK ile signed URL üret (örn. 1 saat)
5. Lisans JSON oluştur + HMAC ile imzala
6. DB'ye kaydet
7. iOS'a döndür

**Response:** License JSON

---

### GET /licenses/:id

**İşlem sırası:**
1. Authorization header'dan Firebase token doğrula
2. Lisansı DB'den çek
3. `expires` kontrolü
4. `userId` eşleşiyor mu kontrol et
5. Yeni signed URL üret (eskisi dolmuş olabilir)
6. Güncel license JSON döndür, geçersizse 403

---

### Gerekli Go Paketleri

```go
import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/hmac"
    "crypto/sha256"
    "crypto/rand"
    "encoding/base64"

    firebase.google.com/go/v4          // Firebase Admin SDK
    firebase.google.com/go/v4/auth     // Token doğrulama
    cloud.google.com/go/storage        // Firebase Storage signed URL
)
```

### Environment Variables (Railway)

```
HMAC_SECRET=<rastgele 32 byte, base64>
FIREBASE_PROJECT_ID=<proje-id>
FIREBASE_SERVICE_ACCOUNT_JSON=<service account JSON string>
FIREBASE_STORAGE_BUCKET=<proje-id>.appspot.com
```

---

### DB Şeması (mevcut DB'ye eklenir)

```sql
-- İçerik anahtarları (asla client'a ham halde gönderilmez)
CREATE TABLE content_keys (
    content_id   TEXT PRIMARY KEY,
    aes_key      TEXT NOT NULL,  -- base64
    iv           TEXT NOT NULL,  -- base64
    created_at   TIMESTAMP DEFAULT NOW()
);

-- Lisanslar
CREATE TABLE licenses (
    id           TEXT PRIMARY KEY,   -- uuid
    user_id      TEXT NOT NULL,
    content_id   TEXT NOT NULL,
    issued_at    TIMESTAMP DEFAULT NOW(),
    expires_at   TIMESTAMP NOT NULL,
    revoked      BOOLEAN DEFAULT FALSE,
    UNIQUE(user_id, content_id)
);
```

---

## Bölüm 3 — iOS: Custom ContentProtection

Readium Swift Toolkit'e eklenecek Swift dosyaları.

### Dosya Yapısı

```
Sources/
  DRM/
    ITechContentProtection.swift
    ITechContentProtectionService.swift
    ITechDecryptor.swift
    LicenseStore.swift          ← Keychain wrapper
    LicenseService.swift        ← Railway API client
    License.swift               ← model
```

---

### License.swift (Model)

```swift
struct License: Codable {
    let id: String
    let userId: String
    let contentId: String
    let contentKey: String   // base64 AES-256 key
    let downloadUrl: String
    let issued: Date
    let expires: Date
    let signature: String
    
    var isExpired: Bool {
        Date() > expires
    }
}
```

---

### ITechContentProtection.swift

```swift
import ReadiumShared
import ReadiumStreamer

final class ITechContentProtection: ContentProtection {

    func open(
        asset: Asset,
        credentials: String?,
        allowUserInteraction: Bool,
        sender: Any?
    ) async -> Result<ProtectedAsset, ContentProtectionOpenError> {

        guard let contentId = extractContentId(from: asset) else {
            return .failure(.assetNotSupported)
        }

        // Keychain'de geçerli lisans var mı?
        if let license = LicenseStore.shared.license(for: contentId), !license.isExpired {
            let decryptor = ITechDecryptor(license: license)
            return .success(ProtectedAsset(asset: asset, decryptor: decryptor))
        }

        // Server'dan yeni lisans al
        do {
            let license = try await LicenseService.shared.fetchLicense(contentId: contentId)
            LicenseStore.shared.save(license)
            let decryptor = ITechDecryptor(license: license)
            return .success(ProtectedAsset(asset: asset, decryptor: decryptor))
        } catch {
            return .failure(.forbidden(error))
        }
    }
}
```

---

### ITechDecryptor.swift

```swift
import CryptoKit
import Foundation

final class ITechDecryptor: ResourceDecryptor {

    private let license: License

    init(license: License) {
        self.license = license
    }

    func decrypt(resource: Resource, using encryption: Encryption) async -> Result<Data, ResourceError> {
        guard let key = Data(base64Encoded: license.contentKey) else {
            return .failure(.decoding(nil))
        }

        do {
            let encrypted = try await resource.read().get()
            let decrypted = try AESCBC.decrypt(data: encrypted, key: key)
            return .success(decrypted)
        } catch {
            return .failure(.decoding(error))
        }
    }
}
```

---

### LicenseStore.swift (Keychain)

```swift
import Security
import Foundation

final class LicenseStore {
    static let shared = LicenseStore()

    func save(_ license: License) {
        guard let data = try? JSONEncoder().encode(license) else { return }
        let key = "itech-license-\(license.contentId)"
        // Keychain'e yaz (kSecClassGenericPassword)
    }

    func license(for contentId: String) -> License? {
        let key = "itech-license-\(contentId)"
        // Keychain'den oku + decode
        // Süresi geçmişse nil döndür
    }
}
```

---

### PublicationOpener Entegrasyonu

```swift
let publicationOpener = PublicationOpener(
    parser: DefaultPublicationParser(
        httpClient: DefaultHTTPClient(),
        assetRetriever: assetRetriever
    ),
    contentProtections: [
        ITechContentProtection()
    ]
)
```

---

## Uygulama Sırası

| Adım | İş | Tahmini Süre |
|------|-----|--------------|
| 1 | EPUB şifreleme scripti (Go) | 1 gün |
| 2 | DB şeması (content_keys + licenses) | yarım gün |
| 3 | POST /licenses endpoint | 1 gün |
| 4 | GET /licenses/:id endpoint | yarım gün |
| 5 | iOS License model + LicenseStore | 1 gün |
| 6 | iOS LicenseService (API client) | 1 gün |
| 7 | iOS ITechDecryptor | 1 gün |
| 8 | iOS ITechContentProtection | 1-2 gün |
| 9 | Entegrasyon testi | 1-2 gün |

**Toplam: ~9-10 gün**

---

## Önemli Notlar

- `contentKey` asla Firebase Storage'a yazılmaz, sadece Railway DB'de durur.
- Offline okuma: Keychain'deki lisans geçerliyse internet olmadan da açılır. Sadece ilk lisans alımında internet şart.
- İleride Cloudflare R2'ya geçildiğinde yalnızca signed URL üretme kısmı değişir, geri kalan mimari aynı kalır.
- Firebase Storage Security Rules'u güncellemeyi unutma: şifreli EPUB'lara public erişim kapalı olmalı.

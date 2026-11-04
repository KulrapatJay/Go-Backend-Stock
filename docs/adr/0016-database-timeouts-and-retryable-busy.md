# ADR-0016: Timeout ของ DB, deadline ต่อคำขอ และ 503 `server_busy` ที่ลองใหม่ได้

- **สถานะ:** Accepted
- **วันที่:** 2026-11-02
- **เกี่ยวข้อง:** ADR-0009, AGENTS.md หัวข้อความสอดคล้องของข้อมูล, milestone 7

## บริบท
`PortfolioRepository.LockOwned` ใช้ `SELECT ... FOR UPDATE` และเดิมรอ row lock ได้ไม่จำกัด ขณะที่ `DB_MAX_CONNS` ตั้งต่ำเพื่อให้พอกับโควตาของ Supabase Free
ถ้าคำขอหนึ่งถือ lock นาน คำขออื่นที่รอพอร์ตเดียวกันจะกิน connection ในพูลจนเต็ม และทำให้คำขออื่นของทุกผู้ใช้รอตามไปด้วย
เมื่อ milestone 7 ถือ lock นานขึ้น (replay ทั้งประวัติ เขียน audit และ idempotency) ความเสี่ยงนี้จะสูงขึ้น
นอกจากนั้น deadlock (`40P01`) และ serialization failure (`40001`) ถูก `mapError` แปลงเป็น 500 `internal` ทั้งที่ลองใหม่ได้ และคำขอไม่มี deadline ของตัวเอง

## ทางเลือกที่พิจารณา
1. ตั้ง timeout ผ่าน env config: ปรับตาม deployment ได้ แต่เพิ่มค่าที่ต้อง validate และค่าที่ผิดเล็กน้อยทำให้เกิดปัญหาเงียบ ๆ (เช่น lock_timeout สูงกว่า deadline ของคำขอ)
2. ค่าคงที่ใน adapter (`lockTimeout` 3 วินาที, `statementTimeout` 5 วินาที, deadline คำขอ 12 วินาที): ค่าสัมพันธ์กันและทดสอบได้ ปรับต้องแก้โค้ด
3. แก้ที่ลำดับ lock อย่างเดียว (ล็อกพอร์ตตามลำดับ id เสมอ): กัน deadlock ของ use case ที่ล็อกหลายพอร์ต แต่ไม่กันการรอ lock นาน และใช้ไม่ได้กับ timeout ของ statement อื่น
4. ลองใหม่เองใน adapter เมื่อชน: ซ่อนปัญหาและยืดเวลาถือ connection; การลองใหม่ควรเป็นของ client ที่รู้ว่า operation ปลอดภัยที่จะซ้ำ (transaction create มี Idempotency-Key)

## การตัดสินใจ
เลือกทางเลือก 2 รวมกับการแปลง error:
- `NewPool` ตั้ง `lock_timeout = 3s` และ `statement_timeout = 5s` ด้วย `AfterConnect` ทุก connection
- middleware `RequestTimeout` ตั้ง deadline 12 วินาทีให้ context ของทุกคำขอ (ต่อจาก `Recover`) ซึ่งสูงกว่าสอง timeout ของ DB เพื่อให้การรอ DB จบด้วย error ของมันเองก่อน
- `mapError` แปลง SQLSTATE `40P01`, `40001`, `55P03`, `57014` และ `context.DeadlineExceeded` เป็น `server_busy` (HTTP 503 พร้อม `Retry-After: 1`); `context.Canceled` ยังเป็น internal เพราะผู้เรียกไปแล้ว; error ของ Begin/Commit ผ่าน `mapError` เช่นกัน
- การลองใหม่ให้ client ทำ ส่วนลำดับ lock ของ use case ที่ล็อกหลายพอร์ตยังไม่มีในเฟส 1 (หนึ่งคำขอล็อกหนึ่งพอร์ต) จึงไม่เพิ่ม; ถ้ามี ให้ล็อกตามลำดับ id และบันทึกใน ADR ใหม่
- ไม่แก้ transaction pooler: session settings ใช้ได้เฉพาะ direct connection หรือ session pooler ตาม AGENTS.md

## ผลที่ตามมา
- คำขอที่รอ lock เกิน 3 วินาทีได้ 503 `server_busy` แทนการค้าง และ connection กลับเข้าพูล
- deadlock และ serialization failure เป็น 503 ที่ลองใหม่ได้ ไม่ใช่ 500; ไม่เขียนข้อมูลเมื่อ error เกิดก่อน commit
- statement ที่ใช้เกิน 5 วินาทีถูกยกเลิกโดย server แม้ client ไปแล้ว จึงต้องแน่ใจว่า migration/maintenance ใช้ role และ connection แยก (ไม่ผ่าน `NewPool`)
- ค่า timeout เป็นค่าคงที่ ต้อง deploy ใหม่เพื่อปรับ
- ทุก operation ของพอร์ตและรายการต้องประกาศ `server_busy` ใน `@Failure 503`

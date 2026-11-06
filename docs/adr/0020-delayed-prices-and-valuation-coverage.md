# ADR-0020: ราคาอ้างอิงแบบหน่วงเวลา และ coverage ของการประเมินมูลค่า

- **สถานะ:** Accepted
- **วันที่:** 2026-11-06
- **เกี่ยวข้อง:** [phase 1 spec](../superpowers/specs/2026-10-05-phase-1-design.md) หัวข้อ 12, [แผน 8b](../superpowers/plans/2026-11-04-milestone-8b-holdings-valuation.md), [แผน 8c](../superpowers/plans/2026-11-05-milestone-8c-alpaca-prices.md), [ADR-0003](0003-decimal-and-event-replay.md), [ADR-0008](0008-instruments-surrogate-key.md), [ADR-0012](0012-defer-stock-page-and-charts.md)

## บริบท
ต้องคำนวณ unrealized P&L ด้วยราคาโดยใช้เฉพาะ Alpaca Basic (ฟรี) ซึ่งอ่านข้อมูล SIP ย้อนหลังได้เมื่อ `end` เก่ากว่า 15 นาที และโควตา 200 ครั้ง/นาที ราคาที่แสดงผิดหรือเป็นศูนย์ทำให้ผู้ใช้เข้าใจพอร์ตผิด หนักกว่าการไม่แสดงราคา และ AGENTS.md ห้ามสลับเป็นข้อมูลเดโมเมื่อ provider ล้มเหลว

## ทางเลือกที่พิจารณา
1. endpoint `latest`/snapshot: ฟรีเฉพาะ IEX ซึ่งไม่ใช่ราคาตลาดรวม และห้ามติดป้ายว่าเป็น delayed SIP
2. แท่ง 1 นาที SIP ย้อนหลังที่จบก่อน "ตอนนี้ − 15 นาที" กรองด้วยเวลา regular session จากปฏิทินตลาดจริง
3. ถอยกลับไปใช้ราคาเดโมเมื่อ provider ล่ม: ไม่ยอมรับ (ข้อมูลปลอมดูเหมือนจริง)
4. ราคาที่ไม่มีเป็น 0: ไม่ยอมรับ (ทำให้ P&L เพี้ยนอย่างเงียบๆ)

## การตัดสินใจ
เลือกข้อ 2
- ราคาอ้างอิง = close ของแท่ง 1 นาที (SIP, `adjustment=raw`) ล่าสุดใน regular session ที่จบไม่เกิน cutoff; `as_of` คือเวลาจบแท่งไม่ใช่เวลาที่ดึง; ตลาดปิด/วันหยุด/ปิดเร็วใช้แท่งปิดของ session ล่าสุดและแสดงเวลาจริง
- ทุก quote บอก `source`, `feed`, `is_demo`, `session_status` (`open`/`market_closed`/`unknown`), `stale` (แท่งเก่ากว่าที่ควรมีเกิน 5 นาที) และ `cached`; ปฏิทินอ่านไม่ได้ → `unknown` + `stale` ไม่เดาวันทำการ และยอมรับว่าแท่งนั้นอาจมาจาก extended hours
- cache ในหน่วยความจำของ process 60 วินาที (กันเกินโควตา) และสำรองไม่เกิน 7 วันเมื่อ Alpaca ล่ม โดยติด `cached` + `stale` กับ `as_of` เดิม; retry 1 ครั้งเฉพาะความล้มเหลวชั่วคราว
- หุ้นที่ไม่มีแท่งใน session อ้างอิง หรือราคาที่ผิดรูป (ศูนย์, ไม่ใช่ USD, symbol ไม่ตรง, `as_of` หลังเวลาคำนวณ) ไม่ถูกเชื่อ: ราคาเป็น `null` พร้อม `unavailable_reason` (`no_price` หรือ `provider_unavailable`)
- `coverage` ของ `GET .../valuation` เป็น `complete` | `partial` | `unavailable`; ไม่ `complete` แล้ว `market_value` กับ `unrealized_pnl` ของทั้งพอร์ตเป็น `null` ส่วน `priced_market_value` เป็นผลรวมเฉพาะตัวที่มีราคา; holdings และ realized P&L ดูได้เสมอ และ valuation ตอบ 200 แม้ provider ล่ม
- ตัวเลขทั้งหมดเป็น decimal string; ราคาถูก parse จาก `json.Number` ไม่ผ่าน float และราคาที่ `ParsePrice` ไม่รับ (exponent, เกิน 10 ตำแหน่ง) ถือว่าไม่มีราคา

## ผลที่ตามมา
- ผู้ใช้ไม่เคยเห็นราคาที่ปลอมหรือเป็นศูนย์แทนที่จะไม่มี; ราคาเก่าบอกอายุตามจริง
- cache หายเมื่อ restart และยังไม่แชร์ข้าม instance (ไม่เพิ่ม Redis ตาม AGENTS.md)
- หุ้นที่ซื้อขายน้อยมากอาจไม่มีแท่งใน session จึงไม่มีราคา แทนที่จะย้อนไปหา session ก่อนหน้าซึ่งต้องขอข้อมูลหลายวันต่อคำขอ
- ราคา live, pre/after-hours, หน้าหุ้นรายตัวและกราฟอยู่เฟสถัดไป (ADR-0012) และต้องตรวจสิทธิ์ feed ของบัญชีอีกครั้ง
- ผลคำนวณไม่หักค่าธรรมเนียมขายในอนาคตและไม่ใช่รายงานภาษี; dividends, splits, mergers และการเปลี่ยน symbol ยังไม่รองรับ

# ADR-0013: Domain value types แบบ parse, don't validate และชนิดตัวเลขแยกตามความหมาย

- **สถานะ:** Accepted
- **วันที่:** 2026-10-16
- **เกี่ยวข้อง:** [milestone 2 design](../superpowers/specs/2026-10-16-milestone-2-domain-ports-design.md), [ADR-0003](0003-decimal-and-event-replay.md), phase 1 spec หัวข้อ 8

## บริบท
core ต้องรับ quantity/price/fee, symbol, email และเวลาที่ผู้ใช้ส่งมา และส่งต่อให้ service/repository หลาย milestone ถ้าตรวจค่าแบบกระจาย จะมีจุดที่ลืมตรวจ และ quantity กับ price ที่เป็น decimal เหมือนกันสลับตำแหน่งกันได้โดย compiler ไม่เตือน

## ทางเลือกที่พิจารณา
1. struct ใช้ชนิดพื้นฐาน + เมธอด `Validate()`: เขียนง่าย แต่ลืมเรียกได้ และค่าที่ตรวจแล้วกับยังไม่ตรวจหน้าตาเหมือนกัน
2. ตรวจใน service อย่างเดียว: domain เป็นแค่ข้อมูล กติกากระจายทุก service
3. parse, don't validate: ค่าดิบแปลงเป็นชนิดที่ตรวจแล้วผ่าน `Parse*` เท่านั้น และตัวเลขแยกชนิด `Quantity`/`Price`/`Fee`

## การตัดสินใจ
ใช้ทางเลือก 3: ชนิดเก็บค่าใน field ที่ไม่ export; `Parse*` รับค่าจากภายนอกและคืน `invalid_input`; `New*` ตรวจค่าที่ server สร้างและคืน `internal`; port signatures ใช้ชนิดที่ตรวจแล้ว (`Email`, `Symbol`, `RevokeReason`, `IdentityProviderName`) แทน `string`

## ผลที่ตามมา
- service และ repository ไม่ต้องตรวจซ้ำ และ compiler กันการสลับ quantity กับ price
- Go ยังสร้าง zero value ได้ (`Quantity{}`) จึงต้องมี `IsZero()` ให้ปฏิเสธ; zero value ของ `Fee` คือค่าธรรมเนียม 0 ที่ถูกต้อง
- adapter ต้อง map ค่าจาก DB ผ่าน `Parse*` อีกครั้ง ค่าที่ผิดกติกาจาก DB ถือเป็น `internal`
- ค่าที่คำนวณ (cost basis, P&L) ใน milestone 7–8 ใช้ `decimal.Decimal` ตรง ไม่สร้างชนิดเพิ่มจนกว่าจะมีเหตุผล

package bacnet

// fail read or write retry count
const retryCount = 3

// writeRetryCount is the number of write attempts, not the number of retries:
// 1 means "try once". Writes are control operations (stop / interlock /
// setpoint) and are not idempotent, so a failed write must NOT be resent
// automatically; it should be reported back to the upper system quickly.
// Reads are idempotent and keep using retryCount.
//
// writeRetryCount 是写尝试次数而非重试次数：1 表示「只尝试一次」。写是控制
// 语义（停机/联锁/设定值）、非幂等，失败不应自动重发，应快速回报给上位系统；
// 读是幂等操作，继续使用 retryCount。
const writeRetryCount = 1

// MTSP
const defaultMTSPBAUD = 38400
const defaultMTSPMAC = 127

// General Bacnet
const defaultMaxMaster = 127
const defaultMaxInfoFrames = 1

// ArrayAll is used when reading/writing to a property to read/write the entire
// array
const ArrayAll = 0xFFFFFFFF
const maxStandardBacnetType = 128

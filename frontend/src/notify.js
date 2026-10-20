// 轻量 toast 通知（替代 window.alert/confirm 弹窗）
import { createDiscreteApi, darkTheme } from 'naive-ui'

const { message } = createDiscreteApi(['message'], { theme: darkTheme })

export function toastInfo(text) {
  message.info(text, { duration: 2600 })
}
export function toastSuccess(text) {
  message.success(text, { duration: 2200 })
}
export function toastError(text) {
  message.error(text, { duration: 3200 })
}

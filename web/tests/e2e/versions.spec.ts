import { expect, test, type Page } from '../support/fixtures'
import type { Route } from '@playwright/test'
import { createAndOpenBook, createProjectFile, readProjectFile, saveProjectFile } from '../support/api'

async function openVersionHistory(page: Page) {
  const saveVersion = page.getByRole('button', { name: '保存当前版本', exact: true })
  if (!(await saveVersion.isVisible())) {
    await page.getByRole('button', { name: '打开版本历史', exact: true }).click()
  }
  await expect(saveVersion).toBeVisible()
  return saveVersion
}

test('saves a version, shows its diff, and restores one file', async ({ page, request }) => {
  const book = await createAndOpenBook(request, 'Versions E2E Book')
  const filePath = 'chapters/versioned.md'
  const original = '# 版本章节\n\n第一个受保护的版本。'
  const changed = '# 版本章节\n\n这是尚未保存为版本的新内容。'
  await createProjectFile(request, book.projectId, filePath, original)

  await page.goto('/')
  const sidebar = page.getByLabel('工作台侧边栏')
  await sidebar.getByRole('button', { name: '版本管理', exact: true }).click()
  await (await openVersionHistory(page)).click()
  await expect(page.getByText(/已保存版本：/)).toBeVisible()

  await saveProjectFile(request, book.projectId, filePath, changed)
  await openVersionHistory(page)
  await page.getByRole('button', { name: '刷新版本状态', exact: true }).first().click()
  await page.getByRole('button', { name: /当前变更/ }).click()
  await expect(page.getByText(filePath, { exact: true }).first()).toBeVisible()

  const restoreFile = page.getByRole('button', { name: '恢复此文件', exact: true })
  await restoreFile.focus()
  await restoreFile.press('Enter')
  const dialog = page.getByRole('alertdialog', { name: '确认恢复文件？' })
  await expect(dialog.getByText(filePath, { exact: true })).toBeVisible()
  await dialog.getByRole('button', { name: '恢复文件', exact: true }).click()

  await expect(page.getByText('已恢复版本', { exact: true })).toBeVisible()
  await expect.poll(async () => (await readProjectFile(request, book.projectId, filePath)).content).toBe(original)
})

test('opens a large version and navigates files before the complete diff arrives', async ({ page, request }) => {
  const book = await createAndOpenBook(request, 'Large version navigation')
  const base = `/api/projects/${book.projectId}`
  const operations = Array.from({ length: 60 }, (_, index) => ({
    id: String(index), kind: 'create', type: 'file',
    path: `chapters/ch${String(index).padStart(3, '0')}-late.md`, content: `# Chapter ${index}\n\nComplete content ${index}.`,
  }))
  const created = await request.post(`${base}/files/operations`, { data: { operations } })
  expect(created.ok(), await created.text()).toBe(true)
  const saved = await request.post(`${base}/versions`, { data: { message: 'Large comparison' } })
  expect(saved.ok(), await saved.text()).toBe(true)
  const version = (await saved.json()).version
  const initial = await (await request.get(`${base}/versions/${version.id}/diff?comparison=parent&view=initial`)).json()
  expect(initial.files_complete).toBe(false)
  expect(initial.files).toHaveLength(1)
  expect(initial.changes.length).toBeGreaterThanOrEqual(60)
  const held: Route[] = []
  let hold = true
  const release = async () => {
    hold = false
    await Promise.all(held.splice(0).map(route => route.continue()))
  }
  await page.route('**/versions/*/diff?*', async route => {
    const url = new URL(route.request().url())
    if (hold && !url.searchParams.has('path') && !url.searchParams.has('view')) {
      held.push(route)
      return
    }
    await route.continue()
  })
  try {
    await page.goto('/')
    await page.getByLabel('工作台侧边栏').getByRole('button', { name: '版本管理', exact: true }).click()
    await expect(page.locator('[data-code-diff-header]').filter({ hasText: initial.files[0].path.split('/').at(-1) })).toBeVisible()
    await page.getByRole('textbox', { name: '筛选文件…', exact: true }).fill('ch059-late')
    await page.getByRole('treeitem', { name: 'ch059-late.md', exact: true }).click({ timeout: 10_000 })
    await expect(page.locator('[data-code-diff-header="chapters/ch059-late.md"]')).toBeVisible()
    await expect(page.getByText('Complete content 59.', { exact: false })).toBeVisible()
    const completed = page.waitForResponse(response => {
      const url = new URL(response.url())
      return url.pathname.endsWith('/diff') && !url.searchParams.has('path') && !url.searchParams.has('view')
    })
    await release()
    expect((await (await completed).json()).files_complete).toBe(true)
    const selectedHeader = page.locator('[data-code-diff-header="chapters/ch059-late.md"]')
    await expect(selectedHeader).toBeVisible()
    await expect(selectedHeader.getByRole('button', { name: '恢复此文件', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: '下一个变更文件', exact: true })).toHaveCount(0)
  } finally {
    await release()
  }
})

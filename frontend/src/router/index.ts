import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'Dashboard',
    component: () => import('@/views/Dashboard.vue'),
    meta: { title: '仪表盘' },
  },
  {
    path: '/books/:id',
    name: 'BookDetail',
    component: () => import('@/views/BookDetail.vue'),
    props: true,
    meta: { title: '书籍详情' },
  },
  {
    path: '/books/:id/chapters/:num',
    name: 'ChapterReader',
    component: () => import('@/views/ChapterReader.vue'),
    props: true,
    meta: { title: '章节阅读' },
  },
  {
    path: '/short-fiction',
    name: 'ShortFiction',
    component: () => import('@/views/ShortFiction.vue'),
    meta: { title: '短篇创建' },
  },
  {
    path: '/settings',
    name: 'Settings',
    component: () => import('@/views/Settings.vue'),
    meta: { title: '设置' },
  },
  {
    path: '/:pathMatch(.*)*',
    redirect: '/',
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.afterEach((to) => {
  const title = (to.meta.title as string) || ''
  document.title = title ? `${title} - inkos 写小说` : 'inkos 写小说系统'
})

export default router

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElContainer, ElHeader, ElAside, ElMain, ElMenu, ElMenuItem, ElButton } from 'element-plus'
import {
  HomeFilled,
  Document,
  Reading,
  Setting,
  MagicStick,
} from '@element-plus/icons-vue'

const route = useRoute()
const router = useRouter()

const activeIndex = computed(() => {
  if (route.path.startsWith('/books')) return '/books'
  if (route.path.startsWith('/short-fiction')) return '/short-fiction'
  if (route.path.startsWith('/settings')) return '/settings'
  return '/'
})

function go(path: string) {
  router.push(path)
}
</script>

<template>
  <el-container class="app-layout">
    <el-header class="app-header">
      <div class="logo" @click="go('/')">
        <el-icon :size="22"><MagicStick /></el-icon>
        <span class="title">inkos 写小说</span>
      </div>
      <el-menu
        :default-active="activeIndex"
        mode="horizontal"
        class="nav-menu"
        :ellipsis="false"
        @select="go"
      >
        <el-menu-item index="/">
          <el-icon><HomeFilled /></el-icon>
          <span>仪表盘</span>
        </el-menu-item>
        <el-menu-item index="/short-fiction">
          <el-icon><Document /></el-icon>
          <span>短篇</span>
        </el-menu-item>
        <el-menu-item index="/settings">
          <el-icon><Setting /></el-icon>
          <span>设置</span>
        </el-menu-item>
      </el-menu>
    </el-header>
    <el-main class="app-main">
      <router-view v-slot="{ Component }">
        <transition name="fade" mode="out-in">
          <component :is="Component" />
        </transition>
      </router-view>
    </el-main>
  </el-container>
</template>

<style scoped>
.app-layout {
  min-height: 100vh;
}
.app-header {
  display: flex;
  align-items: center;
  background: var(--el-color-primary);
  color: #fff;
  padding: 0 24px;
  height: 56px;
}
.app-header .logo {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  margin-right: 32px;
}
.app-header .logo .title {
  font-size: 18px;
  font-weight: 600;
}
.app-header :deep(.nav-menu) {
  background: transparent;
  border-bottom: none;
  flex: 1;
}
.app-header :deep(.el-menu-item) {
  color: rgba(255, 255, 255, 0.85);
  border-bottom-color: transparent;
}
.app-header :deep(.el-menu-item.is-active),
.app-header :deep(.el-menu-item:hover) {
  color: #fff;
  background: rgba(255, 255, 255, 0.15);
  border-bottom-color: #fff;
}
.app-main {
  padding: 0;
  background: var(--el-bg-color-page);
}
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>

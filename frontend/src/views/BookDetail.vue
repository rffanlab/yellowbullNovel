<script setup lang="ts">
import { onMounted, ref, computed, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import {
  ElAside,
  ElButton,
  ElButtonGroup,
  ElContainer,
  ElDivider,
  ElEmpty,
  ElInput,
  ElMain,
  ElScrollbar,
  ElTabPane,
  ElTabs,
  ElTag,
  ElMessage,
} from 'element-plus'
import {
  Promotion,
  Setting as SettingIcon,
  Connection,
  Edit,
  VideoPause,
} from '@element-plus/icons-vue'
import { useBookStore } from '@/stores/book'
import { useChatStore } from '@/stores/chat'
import type { WriteAction } from '@/api/write'
import ChapterList from '@/components/ChapterList.vue'
import WriteProgress from '@/components/WriteProgress.vue'
import HookPanel from '@/components/HookPanel.vue'
import TruthFilesViewer from '@/components/TruthFilesViewer.vue'

const route = useRoute()
const bookStore = useBookStore()
const chatStore = useChatStore()

const bookId = computed(() => route.params.id as string)
const activeTab = ref('chapters')
const inputText = ref('')
const messageScrollRef = ref<HTMLElement | null>(null)

const writing = computed(() => chatStore.writing)

const actionLabels: Record<WriteAction, string> = {
  next_chapter: '写下一章',
  draft: '仅写草稿',
  plan: '规划',
  assemble: '组装',
}

async function loadData() {
  if (!bookId.value) return
  try {
    await Promise.all([
      bookStore.fetchAll(bookId.value),
      chatStore.fetchHistory(bookId.value),
    ])
  } catch {
    // ignore
  }
  await nextTick()
  scrollToBottom()
}

function scrollToBottom() {
  if (messageScrollRef.value) {
    messageScrollRef.value.scrollTop = messageScrollRef.value.scrollHeight
  }
}

async function sendInstruction(action: WriteAction) {
  const instruction = inputText.value.trim()
  if (!instruction && action !== 'next_chapter') {
    ElMessage.warning('请输入指令')
    return
  }
  chatStore.pushUserMessage(instruction || actionLabels[action], action)
  inputText.value = ''
  chatStore.startStream(
    bookId.value,
    {
      action,
      instruction: instruction || undefined,
    },
    async () => {
      // 写作完成后刷新章节列表与进度
      try {
        await bookStore.fetchChapters(bookId.value)
        await bookStore.fetchBook(bookId.value)
        await bookStore.fetchHooks(bookId.value)
      } catch {
        // ignore
      }
    }
  )
}

function stopWriting() {
  chatStore.stopStream()
  ElMessage.info('已停止')
}

function onInputKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    sendInstruction('next_chapter')
  }
}

function onInputKeydownHandler(e: Event) {
  onInputKeydown(e as KeyboardEvent)
}

onMounted(loadData)
</script>

<template>
  <el-container class="book-detail">
    <!-- 左侧侧栏 -->
    <el-aside width="280px" class="sidebar">
      <div class="sidebar-header">
        <span class="book-name" :title="bookStore.currentBook?.title">
          {{ bookStore.currentBook?.title || '加载中...' }}
        </span>
        <el-tag size="small" effect="plain">
          {{ bookStore.currentChapterNum }} / {{ bookStore.currentBook?.target_chapters || 0 }}
        </el-tag>
      </div>
      <el-tabs v-model="activeTab" class="sidebar-tabs">
        <el-tab-pane label="章节" name="chapters">
          <ChapterList
            :chapters="bookStore.chapters"
            :book-id="bookId"
          />
        </el-tab-pane>
        <el-tab-pane label="角色" name="characters">
          <el-scrollbar height="calc(100vh - 200px)">
            <div v-if="bookStore.characters.length">
              <div
                v-for="c in bookStore.characters"
                :key="c.id"
                class="character-item"
              >
                <div class="name">{{ c.name }}</div>
                <el-tag size="small" type="info" effect="plain">
                  {{ c.role }}
                </el-tag>
                <p class="desc">{{ c.description }}</p>
              </div>
            </div>
            <el-empty v-else description="暂无角色" :image-size="60" />
          </el-scrollbar>
        </el-tab-pane>
        <el-tab-pane label="伏笔" name="hooks">
          <HookPanel :hooks="bookStore.hooks" />
        </el-tab-pane>
        <el-tab-pane label="设定" name="settings">
          <el-scrollbar height="calc(100vh - 200px)">
            <div class="setting-info">
              <div class="info-row">
                <span class="label">题材</span>
                <span>{{ bookStore.currentBook?.genre || '-' }}</span>
              </div>
              <div class="info-row">
                <span class="label">平台</span>
                <span>{{ bookStore.currentBook?.platform || '-' }}</span>
              </div>
              <div class="info-row">
                <span class="label">语言</span>
                <span>{{ bookStore.currentBook?.language || '-' }}</span>
              </div>
              <div class="info-row">
                <span class="label">目标章数</span>
                <span>{{ bookStore.currentBook?.target_chapters || 0 }}</span>
              </div>
              <div class="info-row">
                <span class="label">每章字数</span>
                <span>{{ bookStore.currentBook?.words_per_chapter || 0 }}</span>
              </div>
              <el-divider />
              <div class="author-intent">
                <div class="label">作者意图</div>
                <p>{{ bookStore.currentBook?.author_intent || '（未设置）' }}</p>
              </div>
            </div>
          </el-scrollbar>
        </el-tab-pane>
        <el-tab-pane label="Truth" name="truth">
          <TruthFilesViewer :files="bookStore.truthFiles" />
        </el-tab-pane>
      </el-tabs>
    </el-aside>

    <!-- 主聊天区 -->
    <el-main class="chat-main">
      <div class="chat-container">
        <!-- 消息列表 -->
        <div ref="messageScrollRef" class="message-list">
          <div class="message-inner">
            <div
              v-for="(msg, idx) in chatStore.messages"
              :key="idx"
              class="message-row"
              :class="msg.role"
            >
              <div class="chat-bubble" :class="msg.role">
                <div v-if="msg.action" class="action-tag">
                  <el-tag size="small" type="info" effect="dark">
                    {{ actionLabels[msg.action] || msg.action }}
                  </el-tag>
                </div>
                {{ msg.content }}
              </div>
            </div>
            <div v-if="chatStore.messages.length === 0" class="empty-block">
              开始你的创作旅程，在下方输入指令并选择操作。
            </div>
          </div>
        </div>

        <!-- 写作进度 -->
        <WriteProgress v-if="writing.active || writing.streamContent" :progress="writing" />

        <!-- 输入区 -->
        <div class="input-area">
          <el-input
            v-model="inputText"
            type="textarea"
            :rows="3"
            placeholder="输入指令（如：让主角获得传承，然后遇到宿敌挑战）"
            resize="none"
            @keydown="onInputKeydownHandler"
            :disabled="chatStore.isWriting"
          />
          <div class="action-buttons">
            <el-button-group>
              <el-button
                type="primary"
                :icon="Promotion"
                :loading="chatStore.isWriting && writing.action === 'next_chapter'"
                :disabled="chatStore.isWriting"
                @click="sendInstruction('next_chapter')"
              >
                写下一章
              </el-button>
              <el-button
                :icon="Edit"
                :loading="chatStore.isWriting && writing.action === 'draft'"
                :disabled="chatStore.isWriting"
                @click="sendInstruction('draft')"
              >
                仅写草稿
              </el-button>
              <el-button
                :icon="SettingIcon"
                :loading="chatStore.isWriting && writing.action === 'plan'"
                :disabled="chatStore.isWriting"
                @click="sendInstruction('plan')"
              >
                规划
              </el-button>
              <el-button
                :icon="Connection"
                :loading="chatStore.isWriting && writing.action === 'assemble'"
                :disabled="chatStore.isWriting"
                @click="sendInstruction('assemble')"
              >
                组装
              </el-button>
            </el-button-group>
            <el-button
              v-if="chatStore.isWriting"
              type="danger"
              :icon="VideoPause"
              @click="stopWriting"
            >
              停止
            </el-button>
          </div>
        </div>
      </div>
    </el-main>
  </el-container>
</template>

<style scoped>
.book-detail {
  height: calc(100vh - 56px);
}
.sidebar {
  background: #fff;
  border-right: 1px solid var(--ink-border);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.sidebar-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px;
  border-bottom: 1px solid var(--ink-border);
}
.sidebar-header .book-name {
  font-size: 15px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sidebar-tabs {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.sidebar-tabs :deep(.el-tabs__content) {
  flex: 1;
  overflow: hidden;
}
.sidebar-tabs :deep(.el-tab-pane) {
  height: calc(100vh - 160px);
}
.character-item {
  padding: 10px 12px;
  border-bottom: 1px solid var(--ink-border);
}
.character-item .name {
  font-weight: 600;
  margin-bottom: 4px;
}
.character-item .desc {
  font-size: 12px;
  color: var(--ink-text-secondary);
  margin: 4px 0 0;
  line-height: 1.5;
}
.setting-info .info-row {
  display: flex;
  justify-content: space-between;
  padding: 6px 0;
  font-size: 13px;
}
.setting-info .info-row .label {
  color: var(--ink-text-secondary);
}
.setting-info .author-intent .label {
  color: var(--ink-text-secondary);
  margin-bottom: 4px;
  font-size: 13px;
}
.setting-info .author-intent p {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
}
.chat-main {
  padding: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.chat-container {
  display: flex;
  flex-direction: column;
  height: 100%;
  max-width: 900px;
  margin: 0 auto;
  width: 100%;
}
.message-list {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
}
.message-inner {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.message-row {
  display: flex;
}
.message-row.user {
  justify-content: flex-end;
}
.message-row.assistant,
.message-row.system {
  justify-content: flex-start;
}
.message-row .action-tag {
  margin-bottom: 4px;
}
.input-area {
  border-top: 1px solid var(--ink-border);
  padding: 12px;
  background: #fff;
}
.action-buttons {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 8px;
}
</style>

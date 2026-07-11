<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  ElButton,
  ElCol,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElOption,
  ElRow,
  ElSelect,
  ElMessage,
  ElMessageBox,
} from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { useBookStore } from '@/stores/book'
import BookCard from '@/components/BookCard.vue'
import type { CreateBookRequest } from '@/api/book'

const router = useRouter()
const bookStore = useBookStore()

const dialogVisible = ref(false)
const submitting = ref(false)

const genres = [
  '都市', '玄幻', '仙侠', '历史', '科幻', '悬疑', '言情', '武侠',
  '游戏', '体育', '现实', '军事', '职场', '轻小说', '二次元',
]

const platforms = ['起点', '番茄', '晋江', '七猫', '飞卢', '通用']

const languages = ['中文', '英文', '日文']

const defaultForm: CreateBookRequest = {
  title: '',
  genre: '都市',
  platform: '通用',
  target_chapters: 100,
  words_per_chapter: 2000,
  language: '中文',
  author_intent: '',
}

const form = reactive<CreateBookRequest>({ ...defaultForm })

const formRef = ref()

const rules = {
  title: [{ required: true, message: '请输入书名', trigger: 'blur' }],
  genre: [{ required: true, message: '请选择题材', trigger: 'change' }],
  target_chapters: [
    { required: true, message: '请输入目标章数', trigger: 'blur' },
  ],
  words_per_chapter: [
    { required: true, message: '请输入每章字数', trigger: 'blur' },
  ],
}

async function loadBooks() {
  try {
    await bookStore.fetchBooks()
  } catch {
    // 拦截器已提示
  }
}

function openCreate() {
  Object.assign(form, defaultForm)
  dialogVisible.value = true
}

async function submitCreate() {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      const book = await bookStore.createBook({ ...form })
      ElMessage.success('创建成功')
      dialogVisible.value = false
      router.push(`/books/${book.id}`)
    } catch {
      // 拦截器已提示
    } finally {
      submitting.value = false
    }
  })
}

async function handleDelete(id: string) {
  try {
    await ElMessageBox.confirm('确认删除该书籍？此操作不可恢复。', '确认删除', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
    await bookStore.deleteBook(id)
    ElMessage.success('已删除')
  } catch {
    // 用户取消
  }
}

onMounted(loadBooks)
</script>

<template>
  <div class="page-container">
    <div class="dashboard-header">
      <h1 class="page-title">书籍仪表盘</h1>
      <div class="header-actions">
        <el-button :icon="Refresh" @click="loadBooks">刷新</el-button>
        <el-button type="primary" :icon="Plus" @click="openCreate">
          创建新书
        </el-button>
      </div>
    </div>

    <el-row v-loading="bookStore.loading" :gutter="16">
      <el-col
        v-for="book in bookStore.books"
        :key="book.id"
        :xs="24"
        :sm="12"
        :md="8"
        :lg="6"
        class="book-col"
      >
        <BookCard :book="book" @delete="handleDelete" />
      </el-col>
      <el-col v-if="!bookStore.loading && !bookStore.books.length" :span="24">
        <div class="empty-block">
          暂无书籍，点击右上角「创建新书」开始创作
        </div>
      </el-col>
    </el-row>

    <!-- 创建书籍对话框 -->
    <el-dialog
      v-model="dialogVisible"
      title="创建新书"
      width="560px"
      :close-on-click-modal="false"
    >
      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        label-width="100px"
      >
        <el-form-item label="书名" prop="title">
          <el-input v-model="form.title" placeholder="请输入书名" />
        </el-form-item>
        <el-form-item label="题材" prop="genre">
          <el-select v-model="form.genre" placeholder="选择题材" filterable>
            <el-option
              v-for="g in genres"
              :key="g"
              :label="g"
              :value="g"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="平台" prop="platform">
          <el-select v-model="form.platform" placeholder="选择平台">
            <el-option
              v-for="p in platforms"
              :key="p"
              :label="p"
              :value="p"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="目标章数" prop="target_chapters">
          <el-input-number
            v-model="form.target_chapters"
            :min="1"
            :max="1000"
            :step="10"
          />
        </el-form-item>
        <el-form-item label="每章字数" prop="words_per_chapter">
          <el-input-number
            v-model="form.words_per_chapter"
            :min="500"
            :max="10000"
            :step="500"
          />
        </el-form-item>
        <el-form-item label="语言" prop="language">
          <el-select v-model="form.language">
            <el-option
              v-for="l in languages"
              :key="l"
              :label="l"
              :value="l"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="作者意图" prop="author_intent">
          <el-input
            v-model="form.author_intent"
            type="textarea"
            :rows="3"
            placeholder="对故事方向、目标读者、爽点设计等的描述（可选）"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitCreate">
          创建
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.dashboard-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}
.dashboard-header .page-title {
  margin: 0;
}
.header-actions {
  display: flex;
  gap: 8px;
}
.book-col {
  margin-bottom: 16px;
}
</style>

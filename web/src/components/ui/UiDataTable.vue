<script setup lang="ts" generic="T extends Record<string, unknown>">
import { computed, ref, watch } from 'vue'
import { ArrowDown, ArrowUp, ChevronsUpDown } from 'lucide-vue-next'
import UiPagination from './UiPagination.vue'
type DataTableColumn<Row> = { key: keyof Row & string; label: string; sortable?: boolean; align?: 'start' | 'center' | 'end'; format?: (value: Row[keyof Row], row: Row) => string | number }
const props = withDefaults(defineProps<{ rows: T[]; columns: DataTableColumn<T>[]; rowKey?: string; pageSize?: number; caption?: string; emptyText?: string; selectable?: boolean; selectLabel?: string; selectRowLabel?: string }>(), { rowKey: 'id', pageSize: 10, caption: '', emptyText: 'No data', selectable: false, selectLabel: 'Select', selectRowLabel: 'Select row' })
const emit = defineEmits<{ rowClick: [row: T]; selectionChange: [rows: T[]] }>()
const page = ref(1)
const sortKey = ref<string>('')
const sortDirection = ref<'asc' | 'desc'>('asc')
const selected = ref(new Set<unknown>())
const sorted = computed(() => { const rows = [...props.rows]; if (!sortKey.value) return rows; return rows.sort((a, b) => { const left = a[sortKey.value]; const right = b[sortKey.value]; const result = String(left ?? '').localeCompare(String(right ?? ''), undefined, { numeric: true }); return sortDirection.value === 'asc' ? result : -result }) })
const totalPages = computed(() => Math.max(1, Math.ceil(sorted.value.length / props.pageSize)))
const visible = computed(() => sorted.value.slice((page.value - 1) * props.pageSize, page.value * props.pageSize))
watch(() => props.rows.length, () => { page.value = Math.min(page.value, totalPages.value) })
function sort(column: DataTableColumn<T>) { if (!column.sortable) return; if (sortKey.value === column.key) sortDirection.value = sortDirection.value === 'asc' ? 'desc' : 'asc'; else { sortKey.value = column.key; sortDirection.value = 'asc' } }
function key(row: T) { return row[props.rowKey] ?? props.rows.indexOf(row) }
function toggle(row: T) { const next = new Set(selected.value); const value = key(row); if (next.has(value)) next.delete(value); else next.add(value); selected.value = next; emit('selectionChange', props.rows.filter(item => next.has(key(item)))) }
function display(column: DataTableColumn<T>, row: T) { const value = row[column.key]; return column.format ? column.format(value, row) : String(value ?? '') }
</script>

<template>
  <div class="ui-data-table">
    <div class="ui-table-wrap">
      <table class="ui-table">
        <caption v-if="caption">
          {{ caption }}
        </caption><thead>
          <tr>
            <th v-if="selectable" scope="col">
              <span class="sr-only">{{ selectLabel }}</span>
            </th><th v-for="column in columns" :key="column.key" scope="col" :data-align="column.align">
              <button v-if="column.sortable" type="button" @click="sort(column)">
                {{ column.label }}<ArrowUp v-if="sortKey === column.key && sortDirection === 'asc'" :size="13" /><ArrowDown v-else-if="sortKey === column.key" :size="13" /><ChevronsUpDown v-else :size="13" />
              </button><template v-else>
                {{ column.label }}
              </template>
            </th>
          </tr>
        </thead><tbody>
          <tr v-for="row in visible" :key="String(key(row))" @click="emit('rowClick', row)">
            <td v-if="selectable">
              <input type="checkbox" :checked="selected.has(key(row))" :aria-label="selectRowLabel" @click.stop @change="toggle(row)" />
            </td><td v-for="column in columns" :key="column.key" :data-align="column.align">
              <slot :name="`cell-${column.key}`" :row="row" :value="row[column.key]">
                {{ display(column, row) }}
              </slot>
            </td>
          </tr><tr v-if="!visible.length">
            <td class="ui-data-table__empty" :colspan="columns.length + (selectable ? 1 : 0)">
              {{ emptyText }}
            </td>
          </tr>
        </tbody>
      </table>
    </div><UiPagination v-if="totalPages > 1" :page="page" :total-pages="totalPages" @update:page="page = $event" />
  </div>
</template>

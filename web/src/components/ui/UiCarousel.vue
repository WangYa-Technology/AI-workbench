<script setup lang="ts" generic="T">
import { computed } from 'vue'
import { ChevronLeft, ChevronRight } from 'lucide-vue-next'
const props = withDefaults(defineProps<{ items: T[]; modelValue?: number; loop?: boolean; label?: string; previousLabel?: string; nextLabel?: string }>(), { modelValue: 0, loop: false, label: 'Carousel', previousLabel: 'Previous slide', nextLabel: 'Next slide' })
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
const current = computed(() => Math.max(0, Math.min(props.items.length - 1, props.modelValue)))
function move(amount: number) { if (!props.items.length) return; const next = current.value + amount; emit('update:modelValue', props.loop ? (next + props.items.length) % props.items.length : Math.max(0, Math.min(props.items.length - 1, next))) }
</script>

<template>
  <section class="ui-carousel" role="region" aria-roledescription="carousel" :aria-label="label" @keydown.left.prevent="move(-1)" @keydown.right.prevent="move(1)">
    <div class="ui-carousel__viewport">
      <div class="ui-carousel__track" :style="{ transform: `translateX(${-current * 100}%)` }">
        <article v-for="(item, index) in items" :key="index" class="ui-carousel__slide" role="group" aria-roledescription="slide" :aria-label="`${index + 1} of ${items.length}`" :aria-hidden="index !== current">
          <slot name="item" :item="item" :index="index" :active="index === current">
            {{ item }}
          </slot>
        </article>
      </div>
    </div><button class="ui-carousel__control" data-side="previous" type="button" :aria-label="previousLabel" :disabled="!loop && current === 0" @click="move(-1)">
      <ChevronLeft :size="17" />
    </button><button class="ui-carousel__control" data-side="next" type="button" :aria-label="nextLabel" :disabled="!loop && current === items.length - 1" @click="move(1)">
      <ChevronRight :size="17" />
    </button><div class="ui-carousel__dots">
      <button v-for="(_, index) in items" :key="index" type="button" :aria-label="`Go to slide ${index + 1}`" :aria-current="index === current ? 'true' : undefined" @click="emit('update:modelValue', index)"></button>
    </div>
  </section>
</template>

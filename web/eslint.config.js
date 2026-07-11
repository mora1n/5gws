import pluginVue from 'eslint-plugin-vue'
import vueTsEslintConfig from '@vue/eslint-config-typescript'

export default [
	{ files: ['**/*.{ts,vue}'] },
	{ ignores: ['dist/**'] },
	...pluginVue.configs['flat/essential'],
	...vueTsEslintConfig(),
	{ rules: { 'vue/multi-word-component-names': 'off' } },
]

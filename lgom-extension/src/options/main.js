import { mount } from 'svelte';
import '../routes/layout.css';
import Options from './Options.svelte';

const target = document.getElementById('app');
if (!target) throw new Error('options root #app missing');

mount(Options, { target });

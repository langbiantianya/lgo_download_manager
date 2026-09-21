import { mount } from 'svelte';
import '../routes/layout.css';
import Popup from './Popup.svelte';

const target = document.getElementById('app');
if (!target) throw new Error('popup root #app missing');

mount(Popup, { target });

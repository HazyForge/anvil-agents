import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject } from 'react';
import { isNearBottom } from '../wrapper/threadSync';

const prefersReducedMotion = () => typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;

/**
 * Keep a transcript on its newest content while the reader is at the bottom,
 * and leave them in place when they scrolled up to read history. Messages
 * that arrive meanwhile are counted so the view can offer a jump back.
 *
 * resetKey: a new conversation (jumps to the bottom at once).
 * contentKey: anything that changes the transcript's height.
 * arrivals: how many items the transcript holds; growth while unpinned is unseen.
 */
export function useStickToBottom(ref: RefObject<HTMLElement | null>, {resetKey, contentKey, arrivals}: {resetKey: string; contentKey: string; arrivals: number}) {
  const pinned = useRef(true);
  const programmatic = useRef(0);
  const lastTop = useRef(0);
  const lastArrivals = useRef(arrivals);
  const [unseen, setUnseen] = useState(0);

  const scrollToEnd = useCallback((smooth: boolean) => {
    const element = ref.current;
    if (!element) return;
    pinned.current = true;
    if (smooth && !prefersReducedMotion()) {
      // Scroll events fired by the animation itself must not unpin the view.
      programmatic.current = Date.now() + 600;
      element.scrollTo({top: element.scrollHeight, behavior: 'smooth'});
    } else {
      element.scrollTop = element.scrollHeight;
    }
    lastTop.current = element.scrollTop;
  }, [ref]);

  useEffect(() => {
    const element = ref.current;
    if (!element) return;
    const onScroll = () => {
      // A smooth scroll to the end only moves down; moving up is the reader.
      const top = element.scrollTop;
      if (top < lastTop.current - 1) programmatic.current = 0;
      lastTop.current = top;
      if (Date.now() < programmatic.current) return;
      const near = isNearBottom(top, element.scrollHeight, element.clientHeight);
      pinned.current = near;
      if (near) setUnseen(0);
    };
    const follow = () => {
      element.scrollTop = element.scrollHeight;
      lastTop.current = element.scrollTop;
    };
    // Content may grow while a smooth scroll runs; finish at the true end.
    const onScrollEnd = () => {
      const wasFollowing = programmatic.current > 0;
      programmatic.current = 0;
      if (wasFollowing && pinned.current) follow();
      else onScroll();
    };
    // Any other growth (runner activity, a streamed line) keeps a pinned
    // view at the end, once per frame.
    let frame = 0;
    const observer = new MutationObserver(() => {
      if (!pinned.current || frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        if (pinned.current && Date.now() >= programmatic.current) follow();
      });
    });
    observer.observe(element, {childList: true, subtree: true, characterData: true});
    element.addEventListener('scroll', onScroll, {passive: true});
    element.addEventListener('scrollend', onScrollEnd, {passive: true});
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
      element.removeEventListener('scroll', onScroll);
      element.removeEventListener('scrollend', onScrollEnd);
    };
  }, [ref]);

  useLayoutEffect(() => {
    lastArrivals.current = arrivals;
    setUnseen(0);
    scrollToEnd(false);
    // Only a new conversation resets the view.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey, scrollToEnd]);

  useLayoutEffect(() => {
    const added = Math.max(0, arrivals - lastArrivals.current);
    lastArrivals.current = arrivals;
    if (pinned.current) scrollToEnd(added > 0);
    else if (added > 0) setUnseen(count => count + added);
    // contentKey stands in for every height change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contentKey]);

  const jumpToBottom = useCallback(() => { setUnseen(0); scrollToEnd(true); }, [scrollToEnd]);
  return {unseen, jumpToBottom};
}

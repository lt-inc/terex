"use client";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import TwitchNav from "@/components/twitch/TwitchNav";
import ConfirmAction from "@/components/ui/ConfirmAction";
import { applyTwitchPolicy, cleanupTwitchStorage, fetchTwitchPolicyPreview, fetchTwitchStorage, keepTwitchMaterial } from "@/lib/api";
import type { TwitchPolicyPreview, TwitchStorage, TwitchStorageItem } from "@/types/twitch";

const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const bytes = (value: number) => new Intl.NumberFormat("ru", { maximumFractionDigits: 2 }).format(value / 1024 ** 3) + " ГиБ";
const stamp = (value?: string) => value ? new Intl.DateTimeFormat("ru", { dateStyle: "short", timeStyle: "short" }).format(new Date(value)) : "—";
const kindLabels: Record<string, string> = { source: "Исходник", render: "Готовый выпуск", rendered: "Готовый выпуск", manifest: "Монтаж", assembly: "Подборка", staging: "Временная загрузка" };

export default function TwitchStoragePage() {
  const [storage, setStorage] = useState<TwitchStorage | null>(null);
  const [preview, setPreview] = useState<TwitchStorage | null>(null);
  const [policy, setPolicy] = useState<TwitchPolicyPreview | null>(null);
  const [busy, setBusy] = useState(false), [error, setError] = useState(""), [notice, setNotice] = useState("");
  const [confirmation, setConfirmation] = useState<"cleanup" | "policy" | null>(null);
  const [page, setPage] = useState(0);
  const load = useCallback(async () => {
    setBusy(true); setError("");
    try {
      const results = await Promise.allSettled([fetchTwitchStorage(), fetchTwitchPolicyPreview()]);
      if (results[0].status === "fulfilled") setStorage(results[0].value);
      else throw results[0].reason;
      if (results[1].status === "fulfilled") setPolicy(results[1].value);
      else setError(`Проверка календаря недоступна: ${results[1].reason instanceof Error ? results[1].reason.message : String(results[1].reason)}`);
    } catch (failure) { setError(failure instanceof Error ? failure.message : String(failure)); }
    finally { setBusy(false); }
  }, []);
  useEffect(() => { void load(); }, [load]);
  async function calculate() {
    setBusy(true); setError(""); setNotice("");
    try { setPreview(await fetchTwitchStorage(true)); setPage(0); }
    catch (failure) { setError(failure instanceof Error ? failure.message : String(failure)); }
    finally { setBusy(false); }
  }
  async function perform(kind: "cleanup" | "policy") {
    setConfirmation(null); setBusy(true); setError(""); setNotice("");
    try {
      if (kind === "policy") {
        await applyTwitchPolicy();
        setNotice("Ограничения свежести включены. Автоматические планы вне правил удержаны; ручные нарушения доступны в календаре для решения оператора.");
      } else {
        const result = await cleanupTwitchStorage(true);
        setNotice(result.dry_run
          ? `Выполнен только расчёт: доступно ${bytes(result.reclaimable_bytes)}. Удаление выключено настройкой сервиса.`
          : `Очистка запущена: ${result.queued} файлов в очереди, ${result.deleted} удалено, ${result.failed} ошибок. Неудачные удаления будут повторены.`);
        setPreview(null);
      }
      await load();
    } catch (failure) { setError(failure instanceof Error ? failure.message : String(failure)); }
    finally { setBusy(false); }
  }
  async function keep(item: TwitchStorageItem, value: boolean) {
    const id = item.kind === "source" ? item.import_ids?.[0] : item.clip_ids?.[0] || item.import_ids?.[0];
    const kind = item.kind === "source" || !item.clip_ids?.length ? "imports" : "clips";
    if (!id) return;
    await changeKeep(kind, id, value);
  }
  async function changeKeep(kind: "imports" | "clips", id: string, value: boolean) {
    setBusy(true); setError(""); setNotice("");
    try {
      await keepTwitchMaterial(kind, id, value);
      setNotice(value ? "Материал и его текущие зависимости сохранены без срока. Свежесть публикации продолжает проверяться." : "Обычный срок восстановлен без обнуления возраста. Общие файлы могут оставаться защищены другими материалами.");
      setPreview(null); await load();
    } catch (failure) { setError(failure instanceof Error ? failure.message : String(failure)); }
    finally { setBusy(false); }
  }
  const items = preview ? preview.candidates || [] : storage?.items || [];
  const violations = policy?.violations || [];
  return <div className="flex flex-col gap-5 p-4 sm:p-6">
    <div className="flex flex-wrap items-center justify-between gap-3"><h1 className="text-xl font-semibold">Хранение Twitch и актуальность календаря</h1><button className={button} disabled={busy} onClick={() => void load()}>Обновить</button></div>
    <TwitchNav />
    <p className="max-w-5xl text-sm text-slate-400">Неиспользованные файлы хранятся 7 дней после импорта, опубликованные — 7 дней после последнего выпуска. Действующие планы, задания и загрузки с неизвестным результатом защищены. Одобрение без назначения даты срок не продлевает. История и ссылки на YouTube остаются после удаления.</p>
    {error && <p role="alert" className="rounded-lg border border-red-800 p-3 text-sm text-red-300">{error}</p>}
    {notice && <p role="status" className="text-sm text-emerald-300">{notice}</p>}
    {!storage && <p className="text-slate-400">{busy ? "Рассчитываем хранение…" : "Сводка пока недоступна."}</p>}
    {storage && <>
      <section className={`rounded-xl border p-4 text-sm ${storage.pressure?.required && ["low", "unknown"].includes(storage.pressure.state) ? "border-amber-800 bg-amber-950/20" : "border-slate-700 bg-slate-800"}`}>
        <h2 className="font-semibold">Свободное место на файловой системе MinIO</h2>
        {storage.pressure && ["ok", "low"].includes(storage.pressure.state) && storage.pressure.capacity_bytes > 0
          ? <p className="mt-2">Свободно {bytes(storage.pressure.available_bytes)} из {bytes(storage.pressure.capacity_bytes)} ({Math.round(storage.pressure.available_bytes / storage.pressure.capacity_bytes * 100)}%). Проверено: {stamp(storage.pressure.observed_at)}.</p>
          : <p className="mt-2 text-slate-300">{storage.pressure?.state === "disabled" || !storage.pressure ? "Контроль свободного места не настроен." : "Нет актуальных данных о свободном месте. Это не означает, что диск свободен или заполнен."}</p>}
        {storage.pressure?.required && ["low", "unknown"].includes(storage.pressure.state) && <p className="mt-2 text-amber-200">Новые скачивания и рендеры приостановлены до восстановления свободного места или достоверных показаний. Начатые публикации продолжаются.</p>}
        {storage.pressure?.reason && <p className="mt-2 text-amber-200">{storage.pressure.reason}</p>}
        <p className="mt-2 text-xs text-slate-400">Это показания файловой системы, общей с другими данными на томе. Объём файлов Twitch показан отдельно. Запрошенный размер PVC не является подтверждением размера диска или жёсткой квотой.</p>
      </section>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {[["Занято файлами Twitch", bytes(storage.physical_bytes)], ["Доступно для очистки", bytes(storage.reclaimable_bytes)], ["Защищённые файлы", String(storage.protected_objects)], ["Удаления: очередь / ошибки", `${storage.pending_deletions} / ${storage.failed_deletions}`]].map(([label, value]) => <div key={label} className="rounded-xl border border-slate-700 bg-slate-800 p-4"><p className="text-xs text-slate-400">{label}</p><p className="mt-2 text-lg font-semibold">{value}</p></div>)}
      </div>
      <section className="rounded-xl border border-slate-700 bg-slate-800 p-4 text-sm">
        <p className={storage.cleanup_enabled ? "text-emerald-300" : "text-amber-200"}>{storage.cleanup_enabled ? "Автоматическая очистка включена: проверка каждый час." : "Режим расчёта: автоматическое и ручное удаление выключены настройкой сервиса."}</p>
        <p className="mt-2 text-xs text-slate-400">Общие файлы считаются один раз. Здесь показаны файлы Twitch, а не весь MinIO и не свободное место диска. Обновлено: {stamp(storage.updated_at)}.</p>
        {!storage.inventory_complete && <p className="mt-2 text-amber-200">Инвентаризация ещё не завершена: объём и список могут быть неполными. Дождитесь полного расчёта перед очисткой.</p>}
        <div className="mt-3 flex flex-wrap gap-2"><button className={button} disabled={busy} onClick={() => void calculate()}>Предварительный расчёт очистки</button>{preview && <button className="rounded-lg bg-red-950 px-3 py-2 text-sm text-red-200 disabled:opacity-40" disabled={busy || !preview.inventory_complete || !preview.candidates?.length} onClick={() => setConfirmation("cleanup")}>{storage.cleanup_enabled ? "Очистить по расчёту" : "Проверить без удаления"}</button>}</div>
        {preview && <p className="mt-3 text-amber-200">Кандидатов: {preview.candidates?.length || 0}, объём: {bytes(preview.reclaimable_bytes)}. Перед удалением сервер ещё раз проверит действующие зависимости.</p>}
      </section>
      <section className="rounded-xl border border-slate-700 bg-slate-800 p-4">
        <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="font-semibold">{preview ? "Файлы, подходящие для очистки" : "Файлы и сроки хранения"}</h2>{preview && <button className={button} onClick={() => { setPreview(null); setPage(0); }}>Показать все файлы</button>}</div>
        <p className="mt-2 text-xs text-slate-400">«Сохранить» закрепляет выбранный материал и его зависимости. Снятие отметки возвращает исходный срок; общий файл может быть закреплён другим материалом. Удалённый файл нельзя просмотреть или использовать для нового монтажа.</p>
        <div className="mt-3 overflow-x-auto"><table className="w-full text-left text-xs"><thead className="text-slate-400"><tr><th className="p-2">Материал</th><th className="p-2">Объём</th><th className="p-2">Состояние / срок</th><th className="p-2">Действие</th></tr></thead><tbody>{items.slice(page * 50, (page + 1) * 50).map((item) => <tr key={`${item.bucket}/${item.object}`} className="border-t border-slate-700"><td className="p-2"><p>{kindLabels[item.kind] || item.kind}</p>{(item.clip_ids || []).map((id) => <Link key={id} href={`/admin/clips/${encodeURIComponent(id)}`} className="mt-1 block text-indigo-300">Выпуск {id.slice(0, 18)}… →</Link>)}{!item.clip_ids?.length && <p className="mt-1 text-slate-500">Исходников: {item.import_ids?.length || 0}</p>}<details className="mt-1 text-slate-500"><summary>Файл</summary><p className="max-w-md break-all">{item.bucket}/{item.object}</p></details></td><td className="p-2 whitespace-nowrap">{bytes(item.size_bytes)}</td><td className="p-2"><p>{item.state === "deleted" ? "Файл удалён по сроку хранения" : item.keep ? "Сохранён без срока" : item.protected_reason || `Удаление после ${stamp(item.expires_at)}`}</p>{item.protected_reason && item.keep && <p className="mt-1 text-slate-400">{item.protected_reason}</p>}{item.state === "deleting" && <p className="mt-1 text-amber-200">Удаление в очереди или повторяется после сбоя.</p>}{item.error && <p className="mt-1 text-red-300">{item.error}</p>}</td><td className="p-2">
          {item.state === "available" && item.kind !== "staging" && !item.keep && !!(item.import_ids?.length || item.clip_ids?.length) && <button className={button} disabled={busy} onClick={() => void keep(item, true)}>Сохранить</button>}
          {(item.pins || []).map((pin) => <button key={pin.kind + pin.id} className={button + " mt-1 block"} disabled={busy} title={pin.id} onClick={() => void changeKeep(pin.kind === "import" ? "imports" : "clips", pin.id, false)}>Снять сохранение {pin.kind === "import" ? "исходника" : "выпуска"} {pin.id.slice(0, 12)}…</button>)}
        </td></tr>)}</tbody></table></div>
        {!items.length && <p className="mt-3 text-sm text-slate-400">{preview ? "Нет файлов, которые можно безопасно удалить." : "Файлы пока не учтены."}</p>}
        {items.length > 50 && <div className="mt-3 flex items-center gap-3 text-xs"><button className={button} disabled={!page} onClick={() => setPage(page - 1)}>Назад</button><span>{page * 50 + 1}–{Math.min(items.length, (page + 1) * 50)} из {items.length}</span><button className={button} disabled={(page + 1) * 50 >= items.length} onClick={() => setPage(page + 1)}>Далее</button></div>}
      </section>
    </>}
    {policy && <section className="rounded-xl border border-slate-700 bg-slate-800 p-4">
      <h2 className="font-semibold">Переход календаря на ограничения свежести</h2>
      <p className="mt-2 text-sm text-slate-400">{policy.mode === "preview" ? "Сначала проверьте существующую очередь. Пока новые правила только рассчитываются." : "Новые правила включены. Ниже — выпуски, требующие решения."} Далёкая очередь не сжимается в ближайшие два дня. Начатые, уже отправленные и неопределённые загрузки автоматически не меняются.</p>
      <p className="mt-2 text-xs text-slate-300">Нарушений: {violations.length}; защищённых загрузок: {violations.filter((item) => item.protected).length}; ручных планов: {violations.filter((item) => !item.automatic && !item.protected).length}.</p>
      {violations.length > 0 && <div className="mt-3 max-h-96 overflow-auto space-y-2">{violations.map((item) => <div key={item.publication_id} className="rounded-lg bg-slate-900/60 p-3 text-xs"><Link className="text-indigo-300" href="/admin/calendar">Публикация №{item.publication_id} · {stamp(item.publish_at)} →</Link><p className="mt-1">{item.message}</p><p className="mt-1 text-slate-400">Исходник создан {stamp(item.source_created_at)}; крайний срок {stamp(item.deadline)}. {item.protected ? "Загрузка защищена; нужна ручная проверка." : item.automatic ? "Автоматический план будет удержан." : "Ручной план: измените дату, отмените или подтвердите исключение в календаре."}</p></div>)}</div>}
      {policy.mode === "preview" && <button className="mt-3 rounded-lg bg-indigo-600 px-3 py-2 text-sm disabled:opacity-40" disabled={busy} onClick={() => setConfirmation("policy")}>Применить ограничения к очереди</button>}
    </section>}
    {confirmation && <ConfirmAction title={confirmation === "cleanup" ? "Запустить очистку Twitch?" : "Включить ограничения свежести?"} message={confirmation === "cleanup" ? "Сервер повторно проверит зависимости и удалит только истёкшие файлы. История сохранится, но просмотр и новый монтаж удалённых файлов станут недоступны. При выключенной очистке будет выполнен только расчёт." : "Автоматические не начавшиеся планы вне правил будут удержаны и освободят места. Ручные планы требуют решения в календаре. Начатые и неопределённые загрузки сохранятся."} confirmLabel={confirmation === "cleanup" ? "Запустить" : "Применить"} onCancel={() => setConfirmation(null)} onConfirm={() => void perform(confirmation)} />}
  </div>;
}

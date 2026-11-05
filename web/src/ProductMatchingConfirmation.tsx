import type { MappingTarget } from "./ProductReview";

type Props = {
  selected: MappingTarget;
  offer: string;
  candidate: boolean;
  evidence: string;
  setEvidence: (value: string) => void;
  create: () => void;
  creating: boolean;
  pending: boolean;
  reviewID: string;
  loading: boolean;
  error: boolean;
  message: string;
  saved: boolean;
  conflict: boolean;
  current: boolean;
  rejected: boolean;
  acknowledge: boolean;
  setAcknowledge: (value: boolean) => void;
  explanation: string;
  setExplanation: (value: string) => void;
};

export function ProductMatchingConfirmation(p: Props) {
  return (
    <section
      className="matching-review-strip"
      aria-label="Подтверждение выбранной пары"
    >
      <div className="matching-review-choice">
        <div>
          <h3>
            {p.saved ? "Соответствие подтверждено" : "Проверьте выбранную пару"}
          </h3>
          <p>
            <strong>{p.offer}</strong>
            <span aria-hidden="true"> → </span>
            {p.selected.name || p.selected.id}
          </p>
        </div>
        {!p.saved && (
          <>
            <label>
              Основание для подтверждения
              <select
                disabled={p.creating || p.pending}
                value={p.evidence}
                onChange={(e) => p.setEvidence(e.target.value)}
              >
                <option value="OPERATOR_ATTESTED">
                  Проверено мной вручную
                </option>
                {p.candidate && (
                  <option value="DETERMINISTIC_CANDIDATE">
                    Совпадение артикула или кода, проверенное мной
                  </option>
                )}
              </select>
            </label>
            <button
              className="matching-review-button"
              disabled={p.creating || p.pending}
              onClick={p.create}
            >
              {p.creating ? "Проверяем…" : "Проверить соответствие"}
            </button>
          </>
        )}
      </div>
      {!p.reviewID && (
        <p className="matching-review-hint">
          {p.candidate
            ? "Артикул или код совпал. Убедитесь, что это один и тот же товар."
            : "Проверьте, что товары представляют одну и ту же позицию."}
        </p>
      )}
      {p.message && <p role="alert">{p.message}</p>}
      {p.reviewID &&
        (p.loading ? (
          <p role="status">Получаем результат проверки…</p>
        ) : p.error ? (
          <p role="alert">
            Проверка недоступна. Если сессия завершилась, войдите снова.
          </p>
        ) : p.saved ? (
          <p role="status" className="mapping-save-success">
            Соответствие сохранено. Это подтверждение не включает публикацию
            остатков.
          </p>
        ) : (
          <>
            {p.conflict && (
              <p role="alert">
                Уже существует конфликтующее соответствие. Подтверждение
                заблокировано.
              </p>
            )}
            {!p.current && (
              <p role="alert">
                Проверка устарела либо данные или соответствие изменились.
                Создайте новую проверку.
              </p>
            )}
            {p.rejected && (
              <p role="alert">
                Подтверждение отклонено. Проверьте выбранную пару и создайте
                новую проверку.
              </p>
            )}
            <div className="matching-review-ack">
              <label>
                <input
                  type="checkbox"
                  checked={p.acknowledge}
                  disabled={p.pending}
                  onChange={(e) => p.setAcknowledge(e.target.checked)}
                />
                Подтверждаю, что это один и тот же товар
              </label>
              <label>
                Комментарий, необязательно
                <input
                  value={p.explanation}
                  disabled={p.pending}
                  maxLength={1000}
                  onChange={(e) => p.setExplanation(e.target.value)}
                  placeholder="Комментарий к сопоставлению"
                />
              </label>
            </div>
            <span className="matching-review-ready">
              Проверка готова к подтверждению
            </span>
          </>
        ))}
    </section>
  );
}

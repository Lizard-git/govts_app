import {Icon} from "../../components/Icon";

export function ChatPanel({channelName, recipientName}: {channelName?: string; recipientName?: string}) {
    return <section className="chat-panel" aria-label={recipientName ? `Личные сообщения: ${recipientName}` : `Чат канала ${channelName}`}>
        <div className="chat-history" aria-label="История сообщений">
            <div className="chat-placeholder"><Icon name="chat"/><h3>{recipientName ? `Личные сообщения: ${recipientName}` : "Чат канала"}</h3><p>{recipientName ? "Отправка личных сообщений будет добавлена позже." : "Обмен сообщениями будет добавлен позже."}</p></div>
        </div>
        <div className="chat-composer" aria-label="Ввод сообщения">
            <input disabled aria-label="Сообщение" placeholder="Чат пока недоступен"/>
            <button disabled type="button">Отправить</button>
        </div>
    </section>;
}

package domain

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/events"
)

// Compose определяет, кого и о чём уведомить по доменному событию.
//
// Правила выбраны так, чтобы пользователь получал одно уведомление на один
// факт: статус заказа клиенту сообщают события order.* (единый источник истины
// о заказе), курьеру — события delivery.* (источник истины о назначении),
// а события payment.succeeded/failed и delivery.picked_up/delivered дублируют
// order.* и уведомлений не порождают.
//
// Возвращает пустой список для событий, не требующих уведомлений, и ошибку
// KindInvalid, если полезную нагрузку не удалось разобрать.
func Compose(e events.Envelope) ([]Draft, error) {
	switch {
	case strings.HasPrefix(e.Type, "order."):
		var p events.OrderPayload
		if err := e.Decode(&p); err != nil {
			return nil, apperr.Wrap(apperr.KindInvalid, "некорректное событие заказа", err)
		}
		return composeOrder(e.Type, p), nil
	case strings.HasPrefix(e.Type, "payment."):
		var p events.PaymentPayload
		if err := e.Decode(&p); err != nil {
			return nil, apperr.Wrap(apperr.KindInvalid, "некорректное событие платежа", err)
		}
		return composePayment(e.Type, p), nil
	case strings.HasPrefix(e.Type, "delivery."):
		var p events.DeliveryPayload
		if err := e.Decode(&p); err != nil {
			return nil, apperr.Wrap(apperr.KindInvalid, "некорректное событие доставки", err)
		}
		return composeDelivery(e.Type, p), nil
	}
	return nil, nil
}

func composeOrder(eventType string, p events.OrderPayload) []Draft {
	num := events.ShortID(p.OrderID)
	var out []msg
	switch eventType {
	case events.OrderCreated:
		out = append(out, msg{p.CustomerID,
			"Заказ №" + num + " оформлен",
			fmt.Sprintf("Заказ в «%s» на сумму %s ожидает оплаты.", p.RestaurantName, Money(p.Total))})
	case events.OrderPaid:
		out = append(out,
			msg{p.CustomerID,
				"Оплата прошла",
				fmt.Sprintf("Заказ №%s оплачен и передан в «%s». Ждём подтверждения ресторана.", num, p.RestaurantName)},
			msg{p.RestaurantOwnerID,
				"Новый заказ №" + num,
				fmt.Sprintf("Поступил оплаченный заказ на сумму %s (%s). Примите его в работу.",
					Money(p.Total), Items(p.ItemsCount))})
	case events.OrderAccepted:
		out = append(out, msg{p.CustomerID,
			"Ресторан готовит ваш заказ",
			fmt.Sprintf("«%s» принял заказ №%s и начал готовить.", p.RestaurantName, num)})
	case events.OrderReady:
		out = append(out, msg{p.CustomerID,
			"Заказ готов, ждём курьера",
			fmt.Sprintf("Заказ №%s готов. Курьер скоро заберёт его из ресторана.", num)})
		if p.CourierID != "" {
			out = append(out, msg{p.CourierID,
				"Заказ №" + num + " готов к выдаче",
				fmt.Sprintf("Заберите заказ в «%s»: %s.", p.RestaurantName, p.RestaurantAddress)})
		}
	case events.OrderInDelivery:
		body := fmt.Sprintf("Курьер забрал заказ №%s и везёт его по адресу: %s.", num, p.DeliveryAddress)
		if p.CourierName != "" {
			body = fmt.Sprintf("Курьер %s забрал заказ №%s и везёт его по адресу: %s.", p.CourierName, num, p.DeliveryAddress)
		}
		out = append(out, msg{p.CustomerID, "Курьер уже в пути", body})
	case events.OrderDelivered:
		out = append(out,
			msg{p.CustomerID,
				"Заказ доставлен. Приятного аппетита!",
				fmt.Sprintf("Заказ №%s доставлен. Спасибо, что выбрали FoodFlow!", num)},
			msg{p.RestaurantOwnerID,
				"Заказ №" + num + " доставлен",
				fmt.Sprintf("Заказ на сумму %s доставлен клиенту.", Money(p.Total))})
	case events.OrderCancelled:
		reason := strings.TrimSpace(p.Reason)
		body := "Заказ отменён."
		if reason != "" {
			body = "Причина: " + reason + "."
		}
		out = append(out, msg{p.CustomerID, "Заказ №" + num + " отменён", body})
		// Ресторан видел заказ, только если он был оплачен (paid/accepted).
		if p.PreviousStatus == "paid" || p.PreviousStatus == "accepted" {
			out = append(out, msg{p.RestaurantOwnerID, "Заказ №" + num + " отменён", body})
		}
		// Курьера уведомляет событие delivery.cancelled.
	}
	return toDrafts(out, p.OrderID)
}

func composePayment(eventType string, p events.PaymentPayload) []Draft {
	if eventType != events.PaymentRefunded {
		return nil // успех и отказ оплаты сообщаются через order.paid / order.cancelled
	}
	return toDrafts([]msg{{p.CustomerID,
		"Средства возвращены",
		fmt.Sprintf("Возврат %s по заказу №%s оформлен. Деньги поступят на карту в течение нескольких дней.",
			Money(p.Amount), events.ShortID(p.OrderID))}}, p.OrderID)
}

func composeDelivery(eventType string, p events.DeliveryPayload) []Draft {
	num := events.ShortID(p.OrderID)
	var out []msg
	switch eventType {
	case events.DeliveryCourierAssigned:
		out = append(out, msg{p.CourierID,
			"Новая доставка",
			fmt.Sprintf("Заказ №%s: «%s», %s → %s.", num, p.RestaurantName, p.RestaurantAddress, p.DeliveryAddress)})
		courier := "курьер"
		if p.CourierName != "" {
			courier = "курьер " + p.CourierName
		}
		out = append(out, msg{p.CustomerID,
			"Курьер назначен",
			fmt.Sprintf("Ваш заказ №%s доставит %s.", num, courier)})
	case events.DeliveryCancelled:
		if p.CourierID != "" {
			out = append(out, msg{p.CourierID,
				"Доставка отменена",
				fmt.Sprintf("Заказ №%s отменён. Вы снова свободны для новых заказов.", num)})
		}
	}
	return toDrafts(out, p.OrderID)
}

// msg — получатель и текст уведомления.
type msg struct {
	userID, title, body string
}

// toDrafts отбрасывает получателей без идентификатора.
func toDrafts(list []msg, orderID string) []Draft {
	out := make([]Draft, 0, len(list))
	for _, d := range list {
		if strings.TrimSpace(d.userID) == "" {
			continue
		}
		out = append(out, Draft{UserID: d.userID, Title: d.title, Body: d.body, OrderID: orderID})
	}
	return out
}

// Money форматирует сумму в копейках: 129700 → «1 297 ₽», 12950 → «129,50 ₽».
// Разряды и знак валюты отделяются неразрывным пробелом.
func Money(kopecks int64) string {
	const nbsp = " "
	sign := ""
	if kopecks < 0 {
		sign = "−"
		kopecks = -kopecks
	}
	rub, kop := kopecks/100, kopecks%100
	digits := strconv.FormatInt(rub, 10)
	var b strings.Builder
	for i, ch := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(nbsp)
		}
		b.WriteRune(ch)
	}
	s := sign + b.String()
	if kop != 0 {
		s += fmt.Sprintf(",%02d", kop)
	}
	return s + nbsp + "₽"
}

// Items склоняет количество позиций: 1 позиция, 2 позиции, 5 позиций.
func Items(n int) string {
	word := "позиций"
	switch {
	case n%100 >= 11 && n%100 <= 14:
	case n%10 == 1:
		word = "позиция"
	case n%10 >= 2 && n%10 <= 4:
		word = "позиции"
	}
	return strconv.Itoa(n) + " " + word
}

package upstream

import "context"

func (c *Client) signNormal(ctx context.Context, activity Activity, req SignRequest, identity signIdentity) (string, error) {
	pairs := []string{"activeId", activity.ID, "courseId", activity.CourseID, "uid", identity.signUID(), "clientip", "", "useragent", "", "latitude", "-1", "longitude", "-1", "appType", "15", "fid", identity.FID}
	if req.PhotoObjectID != "" {
		pairs = append(pairs, "objectId", req.PhotoObjectID)
	}
	pairs = append(pairs, "name", identity.Name)
	if req.CaptchaValidate != "" {
		pairs = append(pairs, "validate", req.CaptchaValidate)
	}
	pairs = append(pairs, "deviceCode", c.device.DeviceCode)
	if req.SignEnc2 != "" {
		pairs = append(pairs, "enc2", req.SignEnc2)
	}
	pairs = appendFaceParameters(pairs, req)
	body, err := c.get(ctx, stuSignAPI+"?"+orderedQuery(pairs...))
	if err != nil {
		return "", err
	}
	return signResponseState(string(body))
}

func appendFaceParameters(pairs []string, req SignRequest) []string {
	if req.FaceEnc != "" && req.FaceObjectID != "" {
		return append(pairs, "currentFaceId", req.FaceObjectID, "ifCFP", "0", "faceEnc", req.FaceEnc, "faceCode", "", "faceEncAid", "")
	}
	return pairs
}
